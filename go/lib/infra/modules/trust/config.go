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

package trust

import (
	"github.com/scionproto/scion/go/lib/infra/modules/itopo"
	"github.com/scionproto/scion/go/lib/snet"
	"github.com/scionproto/scion/go/proto"
)

// FIXME(scrye): When reloading support gets added again, Options should include
// all the reloadable aspects of the trust store. Instead of direct access,
// accessors should be preferred to ensure concurrency-safe reads.

type Config struct {
	// MustHaveLocalChain states that chain requests for the trust store's own
	// IA must always return a valid chain. This is set to true on infra
	// services BS, CS, PS and false on others.
	MustHaveLocalChain bool
	// ServiceType is the type of the service that uses the store.
	ServiceType proto.ServiceType
	// Router is used to determine paths to other ASes.
	Router snet.Router
	// TopoProvider provides the local topology.
	TopoProvider itopo.ProviderI
}
