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
	"context"
	"flag"
	"fmt"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/opentracing/opentracing-go"

	"github.com/scionproto/scion/go/lib/addr"
	"github.com/scionproto/scion/go/lib/common"
	"github.com/scionproto/scion/go/lib/discovery"
	"github.com/scionproto/scion/go/lib/env"
	"github.com/scionproto/scion/go/lib/fatal"
	"github.com/scionproto/scion/go/lib/infra/infraenv"
	"github.com/scionproto/scion/go/lib/infra/messenger"
	"github.com/scionproto/scion/go/lib/infra/modules/idiscovery"
	"github.com/scionproto/scion/go/lib/infra/modules/itopo"
	"github.com/scionproto/scion/go/lib/infra/modules/segfetcher"
	"github.com/scionproto/scion/go/lib/infra/modules/trust"
	"github.com/scionproto/scion/go/lib/log"
	"github.com/scionproto/scion/go/lib/pathdb"
	"github.com/scionproto/scion/go/lib/pathstorage"
	"github.com/scionproto/scion/go/lib/periodic"
	"github.com/scionproto/scion/go/lib/prom"
	"github.com/scionproto/scion/go/lib/revcache"
	"github.com/scionproto/scion/go/proto"
	"github.com/scionproto/scion/go/sciond/internal/config"
	"github.com/scionproto/scion/go/sciond/internal/fetcher"
	"github.com/scionproto/scion/go/sciond/internal/servers"
)

const (
	ShutdownWaitTimeout = 5 * time.Second
)

var (
	cfg         config.Config
	discRunners idiscovery.Runners
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
	defer env.LogAppStopped("SD", cfg.General.ID)
	defer log.LogPanicAndExit()
	if err := setup(); err != nil {
		log.Crit("Setup failed", "err", err)
		return 1
	}
	if err := startDiscovery(cfg.Discovery); err != nil {
		log.Crit("Unable to start topology fetcher", "err", err)
		return 1
	}
	pathDB, revCache, err := pathstorage.NewPathStorage(cfg.SD.PathDB, cfg.SD.RevCache)
	if err != nil {
		log.Crit("Unable to initialize path storage", "err", err)
		return 1
	}
	defer pathDB.Close()
	defer revCache.Close()
	trustDB, err := cfg.TrustDB.New()
	if err != nil {
		log.Crit("Unable to initialize trustDB", "err", err)
		return 1
	}
	defer trustDB.Close()
	trustConf := trust.Config{TopoProvider: itopo.Provider()}
	trustStore := trust.NewStore(trustDB, itopo.Get().IA(), trustConf, log.Root())
	err = trustStore.LoadAuthoritativeTRC(filepath.Join(cfg.General.ConfigDir, "certs"))
	if err != nil {
		log.Crit("Unable to load local TRC", "err", err)
		return 1
	}
	tracer, trCloser, err := cfg.Tracing.NewTracer(cfg.General.ID)
	if err != nil {
		log.Crit("Unable to create tracer", "err", err)
		return 1
	}
	defer trCloser.Close()
	opentracing.SetGlobalTracer(tracer)
	nc := infraenv.NetworkConfig{
		IA:                    itopo.Get().IA(),
		Public:                cfg.SD.Public,
		Bind:                  cfg.SD.Bind,
		SVC:                   addr.SvcNone,
		ReconnectToDispatcher: cfg.General.ReconnectToDispatcher,
		QUIC: infraenv.QUIC{
			Address:  cfg.QUIC.Address,
			CertFile: cfg.QUIC.CertFile,
			KeyFile:  cfg.QUIC.KeyFile,
		},
		SVCResolutionFraction: cfg.QUIC.ResolutionFraction,
		TrustStore:            trustStore,
		SVCRouter:             messenger.NewSVCRouter(itopo.Provider()),
	}
	msger, err := nc.Messenger()
	if err != nil {
		log.Crit(infraenv.ErrAppUnableToInitMessenger.Error(), "err", err)
		return 1
	}
	// Route messages to their correct handlers
	handlers := servers.HandlerMap{
		proto.SCIONDMsg_Which_pathReq: &servers.PathRequestHandler{
			Fetcher: fetcher.NewFetcher(
				msger,
				pathDB,
				trustStore,
				revCache,
				cfg.SD,
				itopo.Provider(),
				log.Root(),
			),
		},
		proto.SCIONDMsg_Which_asInfoReq: &servers.ASInfoRequestHandler{
			ASInspector: trustStore,
		},
		proto.SCIONDMsg_Which_ifInfoRequest:      &servers.IFInfoRequestHandler{},
		proto.SCIONDMsg_Which_serviceInfoRequest: &servers.SVCInfoRequestHandler{},
		proto.SCIONDMsg_Which_revNotification: &servers.RevNotificationHandler{
			RevCache:         revCache,
			VerifierFactory:  trustStore,
			NextQueryCleaner: segfetcher.NextQueryCleaner{PathDB: pathDB},
		},
	}
	cleaner := periodic.Start(pathdb.NewCleaner(pathDB, "sd_segments"),
		300*time.Second, 295*time.Second)
	defer cleaner.Stop()
	rcCleaner := periodic.Start(revcache.NewCleaner(revCache, "sd_revocation"),
		10*time.Second, 10*time.Second)
	defer rcCleaner.Stop()
	// Start servers
	rsockServer, shutdownF := NewServer("rsock", cfg.SD.Reliable, handlers, log.Root())
	defer shutdownF()
	StartServer("ReliableSockServer", cfg.SD.Reliable, rsockServer)
	unixpacketServer, shutdownF := NewServer("unixpacket", cfg.SD.Unix, handlers, log.Root())
	defer shutdownF()
	StartServer("UnixServer", cfg.SD.Unix, unixpacketServer)
	cfg.Metrics.StartPrometheus()
	select {
	case <-fatal.ShutdownChan():
		// Whenever we receive a SIGINT or SIGTERM we exit without an error.
		// Deferred shutdowns for all running servers run now.
		return 0
	case <-fatal.FatalChan():
		return 1
	}
}

func setupBasic() error {
	if _, err := toml.DecodeFile(env.ConfigFile(), &cfg); err != nil {
		return err
	}
	cfg.InitDefaults()
	if err := env.InitLogging(&cfg.Logging); err != nil {
		return err
	}
	prom.ExportElementID(cfg.General.ID)
	return env.LogAppStarted("SD", cfg.General.ID)
}

func setup() error {
	if err := cfg.Validate(); err != nil {
		return common.NewBasicError("unable to validate config", err)
	}
	itopo.Init("", proto.ServiceType_unset, itopo.Callbacks{})
	topo, err := itopo.LoadFromFile(cfg.General.Topology)
	if err != nil {
		return common.NewBasicError("unable to load topology", err)
	}
	if _, _, err := itopo.SetStatic(topo, false); err != nil {
		return common.NewBasicError("unable to set initial static topology", err)
	}
	infraenv.InitInfraEnvironment(cfg.General.Topology)
	return cfg.SD.CreateSocketDirs()
}

func startDiscovery(file idiscovery.Config) error {
	var err error
	discRunners, err = idiscovery.StartRunners(file, discovery.Default,
		idiscovery.TopoHandlers{}, nil, "sd")
	return err
}

func NewServer(network string, rsockPath string, handlers servers.HandlerMap,
	logger log.Logger) (*servers.Server, func()) {

	server := servers.NewServer(network, rsockPath, os.FileMode(cfg.SD.SocketFileMode), handlers,
		logger)
	shutdownF := func() {
		ctx, cancelF := context.WithTimeout(context.Background(), ShutdownWaitTimeout)
		server.Shutdown(ctx)
		cancelF()
	}
	return server, shutdownF
}

func StartServer(name, sockPath string, server *servers.Server) {
	go func() {
		defer log.LogPanicAndExit()
		if cfg.SD.DeleteSocket {
			if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
				fatal.Fatal(common.NewBasicError("SocketRemoval error", err, "name", name))
			}
		}
		if err := server.ListenAndServe(); err != nil {
			fatal.Fatal(common.NewBasicError("ListenAndServe error", err, "name", name))
		}
	}()
}
