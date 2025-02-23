// Copyright 2018 Anapaya Systems
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
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/opentracing/opentracing-go"

	"github.com/scionproto/scion/go/lib/addr"
	"github.com/scionproto/scion/go/lib/common"
	"github.com/scionproto/scion/go/lib/discovery"
	"github.com/scionproto/scion/go/lib/env"
	"github.com/scionproto/scion/go/lib/fatal"
	"github.com/scionproto/scion/go/lib/infra"
	"github.com/scionproto/scion/go/lib/infra/infraenv"
	"github.com/scionproto/scion/go/lib/infra/messenger"
	"github.com/scionproto/scion/go/lib/infra/modules/idiscovery"
	"github.com/scionproto/scion/go/lib/infra/modules/itopo"
	"github.com/scionproto/scion/go/lib/infra/modules/trust"
	"github.com/scionproto/scion/go/lib/infra/modules/trust/trustdb"
	"github.com/scionproto/scion/go/lib/log"
	"github.com/scionproto/scion/go/lib/pathdb"
	"github.com/scionproto/scion/go/lib/pathstorage"
	"github.com/scionproto/scion/go/lib/periodic"
	"github.com/scionproto/scion/go/lib/prom"
	"github.com/scionproto/scion/go/lib/revcache"
	"github.com/scionproto/scion/go/path_srv/internal/config"
	"github.com/scionproto/scion/go/path_srv/internal/cryptosyncer"
	"github.com/scionproto/scion/go/path_srv/internal/handlers"
	"github.com/scionproto/scion/go/path_srv/internal/segreq"
	"github.com/scionproto/scion/go/path_srv/internal/segsyncer"
	"github.com/scionproto/scion/go/proto"
)

var (
	cfg   config.Config
	tasks *periodicTasks
)

func init() {
	flag.Usage = env.Usage
}

// main initializes the path server and starts the dispatcher.
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
	defer env.LogAppStopped(common.PS, cfg.General.ID)
	defer log.LogPanicAndExit()
	if err := setup(); err != nil {
		log.Crit("Setup failed", "err", err)
		return 1
	}
	pathDB, revCache, err := pathstorage.NewPathStorage(cfg.PS.PathDB, cfg.PS.RevCache)
	if err != nil {
		log.Crit("Unable to initialize path storage", "err", err)
		return 1
	}
	defer revCache.Close()
	pathDB = pathdb.WithMetrics("std", pathDB)
	defer pathDB.Close()
	trustDB, err := cfg.TrustDB.New()
	if err != nil {
		log.Crit("Unable to initialize trustDB", "err", err)
		return 1
	}
	trustDB = trustdb.WithMetrics(string(cfg.TrustDB.Backend()), trustDB)
	defer trustDB.Close()
	topo := itopo.Get()
	trustConf := trust.Config{
		MustHaveLocalChain: true,
		ServiceType:        proto.ServiceType_ps,
		TopoProvider:       itopo.Provider(),
	}
	trustStore := trust.NewStore(trustDB, topo.IA(), trustConf, log.Root())
	err = trustStore.LoadAuthoritativeCrypto(filepath.Join(cfg.General.ConfigDir, "certs"))
	if err != nil {
		log.Crit("Unable to load local crypto", "err", err)
		return 1
	}
	if !topo.Exists(addr.SvcPS, cfg.General.ID) {
		log.Crit("Unable to find topo address")
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
		IA:                    topo.IA(),
		Public:                topo.SPublicAddress(addr.SvcPS, cfg.General.ID),
		Bind:                  topo.SBindAddress(addr.SvcPS, cfg.General.ID),
		SVC:                   addr.SvcPS,
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
	defer msger.CloseServer()
	msger.AddHandler(infra.ChainRequest, trustStore.NewChainReqHandler(false))
	// TODO(lukedirtwalker): with the new CP-PKI design the PS should no longer need to handle TRC
	// and cert requests.
	msger.AddHandler(infra.TRCRequest, trustStore.NewTRCReqHandler(false))
	args := handlers.HandlerArgs{
		PathDB:          pathDB,
		RevCache:        revCache,
		ASInspector:     trustStore,
		VerifierFactory: trustStore,
		QueryInterval:   cfg.PS.QueryInterval.Duration,
		IA:              topo.IA(),
		TopoProvider:    itopo.Provider(),
		SegRequestAPI:   msger,
	}
	msger.AddHandler(infra.SegRequest, segreq.NewHandler(args))
	msger.AddHandler(infra.SegReg, handlers.NewSegRegHandler(args))
	if cfg.PS.SegSync && topo.Core() {
		// Old down segment sync mechanism
		msger.AddHandler(infra.SegSync, handlers.NewSyncHandler(args))
	}
	msger.AddHandler(infra.SignedRev, handlers.NewRevocHandler(args))
	cfg.Metrics.StartPrometheus()
	// Start handling requests/messages
	go func() {
		defer log.LogPanicAndExit()
		msger.ListenAndServe()
	}()
	discoRunners, err := idiscovery.StartRunners(cfg.Discovery, discovery.Full,
		idiscovery.TopoHandlers{}, nil, "ps")
	if err != nil {
		log.Crit("Unable to start topology fetcher", "err", err)
		return 1
	}
	defer discoRunners.Kill()
	tasks = &periodicTasks{
		args:    args,
		msger:   msger,
		trustDB: trustDB,
	}
	if err := tasks.Start(); err != nil {
		log.Crit("Failed to start periodic tasks", "err", err)
		return 1
	}
	defer tasks.Kill()
	select {
	case <-fatal.ShutdownChan():
		// Whenever we receive a SIGINT or SIGTERM we exit without an error.
		return 0
	case <-fatal.FatalChan():
		return 1
	}
}

type periodicTasks struct {
	args          handlers.HandlerArgs
	msger         infra.Messenger
	trustDB       trustdb.TrustDB
	mtx           sync.Mutex
	running       bool
	segSyncers    []*periodic.Runner
	pathDBCleaner *periodic.Runner
	cryptosyncer  *periodic.Runner
	rcCleaner     *periodic.Runner
}

func (t *periodicTasks) Start() error {
	t.mtx.Lock()
	defer t.mtx.Unlock()
	if t.running {
		log.Warn("Trying to start tasks, but they are running! Ignored.")
		return nil
	}
	t.running = true
	var err error
	if cfg.PS.SegSync && itopo.Get().Core() {
		t.segSyncers, err = segsyncer.StartAll(t.args, t.msger)
		if err != nil {
			return common.NewBasicError("Unable to start seg syncer", err)
		}
	}
	t.pathDBCleaner = periodic.Start(pathdb.NewCleaner(t.args.PathDB, "ps_segments"),
		300*time.Second, 295*time.Second)
	t.cryptosyncer = periodic.Start(&cryptosyncer.Syncer{
		DB:    t.trustDB,
		Msger: t.msger,
		IA:    t.args.IA,
	}, cfg.PS.CryptoSyncInterval.Duration, cfg.PS.CryptoSyncInterval.Duration)
	t.rcCleaner = periodic.Start(revcache.NewCleaner(t.args.RevCache, "ps_revocation"),
		10*time.Second, 10*time.Second)
	return nil
}

func (t *periodicTasks) Kill() {
	t.mtx.Lock()
	defer t.mtx.Unlock()
	if !t.running {
		log.Warn("Trying to stop tasks, but they are not running! Ignored.")
		return
	}
	for i := range t.segSyncers {
		syncer := t.segSyncers[i]
		syncer.Kill()
	}
	t.pathDBCleaner.Kill()
	t.cryptosyncer.Kill()
	t.rcCleaner.Kill()
	t.running = false
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
	return env.LogAppStarted(common.PS, cfg.General.ID)
}

func setup() error {
	if err := cfg.Validate(); err != nil {
		return common.NewBasicError("Unable to validate config", err)
	}
	itopo.Init(cfg.General.ID, proto.ServiceType_ps, itopo.Callbacks{})
	topo, err := itopo.LoadFromFile(cfg.General.Topology)
	if err != nil {
		return common.NewBasicError("Unable to load topology", err)
	}
	if _, _, err := itopo.SetStatic(topo, false); err != nil {
		return common.NewBasicError("Unable to set initial static topology", err)
	}
	infraenv.InitInfraEnvironment(cfg.General.Topology)
	return nil
}
