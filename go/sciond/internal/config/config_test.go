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

package config

import (
	"bytes"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"

	"github.com/scionproto/scion/go/lib/env/envtest"
	"github.com/scionproto/scion/go/lib/infra/modules/idiscovery/idiscoverytest"
	"github.com/scionproto/scion/go/lib/pathstorage/pathstoragetest"
	"github.com/scionproto/scion/go/lib/sciond"
	"github.com/scionproto/scion/go/lib/truststorage/truststoragetest"
)

func TestConfigSample(t *testing.T) {
	var sample bytes.Buffer
	var cfg Config
	cfg.Sample(&sample, nil, nil)

	InitTestConfig(&cfg)
	meta, err := toml.Decode(sample.String(), &cfg)
	assert.NoError(t, err)
	assert.Empty(t, meta.Undecoded())
	CheckTestConfig(t, &cfg, idSample)
}

func InitTestConfig(cfg *Config) {
	envtest.InitTest(&cfg.General, &cfg.Logging, &cfg.Metrics, &cfg.Tracing, nil)
	truststoragetest.InitTestConfig(&cfg.TrustDB)
	idiscoverytest.InitTestConfig(&cfg.Discovery)
	InitTestSDConfig(&cfg.SD)
}

func InitTestSDConfig(cfg *SDConfig) {
	cfg.DeleteSocket = true
	pathstoragetest.InitTestPathDBConf(&cfg.PathDB)
	pathstoragetest.InitTestRevCacheConf(&cfg.RevCache)
}

func CheckTestConfig(t *testing.T, cfg *Config, id string) {
	envtest.CheckTest(t, &cfg.General, &cfg.Logging, &cfg.Metrics, &cfg.Tracing, nil, id)
	truststoragetest.CheckTestConfig(t, &cfg.TrustDB, id)
	idiscoverytest.CheckTestConfig(t, &cfg.Discovery)
	CheckTestSDConfig(t, &cfg.SD, id)
}

func CheckTestSDConfig(t *testing.T, cfg *SDConfig, id string) {
	pathstoragetest.CheckTestPathDBConf(t, &cfg.PathDB, id)
	pathstoragetest.CheckTestRevCacheConf(t, &cfg.RevCache)
	assert.Equal(t, sciond.DefaultSCIONDPath, cfg.Reliable)
	assert.Equal(t, "/run/shm/sciond/default-unix.sock", cfg.Unix)
	assert.Equal(t, sciond.DefaultSocketFileMode, int(cfg.SocketFileMode))
	assert.Equal(t, "1-ff00:0:110,[127.0.0.1]:0", cfg.Public.String())
	assert.Equal(t, DefaultQueryInterval, cfg.QueryInterval.Duration)
	assert.False(t, cfg.DeleteSocket)
}
