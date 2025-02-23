// Copyright 2017 ETH Zurich
// Copyright 2018 ETH Zurich, Anapaya Systems
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"flag"
	"fmt"
	"io"
	_ "net/http/pprof"
	"os"
	"os/user"
	"sync/atomic"

	"github.com/BurntSushi/toml"
	"github.com/syndtr/gocapability/capability"

	"github.com/scionproto/scion/go/lib/common"
	"github.com/scionproto/scion/go/lib/env"
	"github.com/scionproto/scion/go/lib/fatal"
	"github.com/scionproto/scion/go/lib/log"
	"github.com/scionproto/scion/go/lib/prom"
	"github.com/scionproto/scion/go/lib/serrors"
	"github.com/scionproto/scion/go/sig/egress"
	"github.com/scionproto/scion/go/sig/internal/base"
	"github.com/scionproto/scion/go/sig/internal/config"
	"github.com/scionproto/scion/go/sig/internal/disp"
	"github.com/scionproto/scion/go/sig/internal/ingress"
	"github.com/scionproto/scion/go/sig/internal/metrics"
	"github.com/scionproto/scion/go/sig/internal/sigconfig"
	"github.com/scionproto/scion/go/sig/internal/xnet"
	"github.com/scionproto/scion/go/sig/sigcmn"
)

var (
	cfg sigconfig.Config
)

func init() {
	flag.Usage = env.Usage
}

func main() {
	os.Exit(realMain())
}

func realMain() int {
	fatal.Init()
	env.AddFlags()
	flag.Parse()
	if v, ok := env.CheckFlags(&cfg); !ok {
		return v
	}
	if err := setupBasic(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer log.Flush()
	defer env.LogAppStopped("SIG", cfg.Sig.ID)
	defer log.LogPanicAndExit()
	if err := validateConfig(); err != nil {
		log.Crit("Validation of config failed", "err", err)
		return 1
	}
	// Setup tun early so that we can drop capabilities before interacting with network etc.
	tunIO, err := setupTun()
	if err != nil {
		log.Crit("Unable to create & configure TUN device", "err", err)
		return 1
	}
	if err := sigcmn.Init(cfg.Sig, cfg.Sciond); err != nil {
		log.Crit("Error during initialization", "err", err)
		return 1
	}
	env.SetupEnv(
		func() {
			success := loadConfig(cfg.Sig.SIGConfig)
			// Errors already logged in loadConfig
			log.Info("reloadOnSIGHUP: reload done", "success", success)
		},
	)
	disp.Init(sigcmn.CtrlConn, false)
	// Parse sig config
	if loadConfig(cfg.Sig.SIGConfig) != true {
		log.Crit("Unable to load sig config on startup")
		return 1
	}
	// Reply to probes from other SIGs.
	go func() {
		defer log.LogPanicAndExit()
		base.PollReqHdlr()
	}()
	egress.Init(tunIO)
	ingress.Init(tunIO)
	cfg.Metrics.StartPrometheus()
	select {
	case <-fatal.ShutdownChan():
		return 0
	case <-fatal.FatalChan():
		return 1
	}
}

// setupBasic loads the config from file and initializes logging.
func setupBasic() error {
	// Load and initialize config.
	if _, err := toml.DecodeFile(env.ConfigFile(), &cfg); err != nil {
		return err
	}
	cfg.InitDefaults()
	if err := env.InitLogging(&cfg.Logging); err != nil {
		return err
	}
	prom.ExportElementID(cfg.Sig.ID)
	return env.LogAppStarted("SIG", cfg.Sig.ID)
}

func validateConfig() error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if cfg.Metrics.Prometheus == "" {
		cfg.Metrics.Prometheus = "127.0.0.1:1281"
	}
	return nil
}

func setupTun() (io.ReadWriteCloser, error) {
	if err := checkPerms(); err != nil {
		return nil, serrors.New("Permissions checks failed")
	}
	tunLink, tunIO, err := xnet.ConnectTun(cfg.Sig.Tun)
	if err != nil {
		return nil, err
	}
	src := cfg.Sig.SrcIP4
	if len(src) == 0 && cfg.Sig.IP.To4() != nil {
		src = cfg.Sig.IP
	}
	if err = xnet.AddRoute(cfg.Sig.TunRTableId, tunLink, sigcmn.DefV4Net, src); err != nil {
		return nil,
			common.NewBasicError("Unable to add default IPv4 route to SIG routing table", err)
	}
	src = cfg.Sig.SrcIP6
	if len(src) == 0 && cfg.Sig.IP.To16() != nil && cfg.Sig.IP.To4() == nil {
		src = cfg.Sig.IP
	}
	if err = xnet.AddRoute(cfg.Sig.TunRTableId, tunLink, sigcmn.DefV6Net, src); err != nil {
		return nil,
			common.NewBasicError("Unable to add default IPv6 route to SIG routing table", err)
	}
	// Now that everything is set up, drop CAP_NET_ADMIN
	caps, err := capability.NewPid(0)
	if err != nil {
		return nil, common.NewBasicError("Error retrieving capabilities", err)
	}
	caps.Clear(capability.CAPS)
	caps.Apply(capability.CAPS)
	return tunIO, nil
}

func checkPerms() error {
	u, err := user.Current()
	if err != nil {
		return common.NewBasicError("Error retrieving user", err)
	}
	if u.Uid == "0" {
		return serrors.New("Running as root is not allowed for security reasons")
	}
	caps, err := capability.NewPid(0)
	if err != nil {
		return common.NewBasicError("Error retrieving capabilities", err)
	}
	log.Info("Startup capabilities", "caps", caps)
	if !caps.Get(capability.EFFECTIVE, capability.CAP_NET_ADMIN) {
		return common.NewBasicError("CAP_NET_ADMIN is required", nil, "caps", caps)
	}
	return nil
}

func loadConfig(path string) bool {
	cfg, err := config.LoadFromFile(path)
	if err != nil {
		log.Error("loadConfig: Failed", "err", err)
		return false
	}
	ok := egress.ReloadConfig(cfg)
	if !ok {
		return false
	}
	atomic.StoreUint64(&metrics.ConfigVersion, cfg.ConfigVersion)
	return true
}
