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

package addrutil

import (
	"bytes"
	"net"

	"github.com/scionproto/scion/go/lib/addr"
	"github.com/scionproto/scion/go/lib/common"
	"github.com/scionproto/scion/go/lib/ctrl/seg"
	"github.com/scionproto/scion/go/lib/infra/modules/itopo"
	"github.com/scionproto/scion/go/lib/snet"
	"github.com/scionproto/scion/go/lib/spath"
)

// GetPath creates a path from the given segment and then creates a snet.Addr.
func GetPath(svc addr.HostSVC, ps *seg.PathSegment, topoProv itopo.ProviderI) (net.Addr, error) {
	x := &bytes.Buffer{}
	if _, err := ps.RawWriteTo(x); err != nil {
		return nil, common.NewBasicError("failed to write segment to buffer", err)
	}
	p := spath.New(x.Bytes())
	if err := p.Reverse(); err != nil {
		return nil, common.NewBasicError("failed to reverse path", err)
	}
	if err := p.InitOffsets(); err != nil {
		return nil, common.NewBasicError("failed to init offsets", err)
	}
	hopF, err := p.GetHopField(p.HopOff)
	if err != nil {
		return nil, common.NewBasicError("failed to extract first HopField", err, "p", p)
	}
	topo := topoProv.Get()
	ifID := hopF.ConsIngress
	overlayNextHop, ok := topo.OverlayNextHop2(ifID)
	if !ok {
		return nil, common.NewBasicError("unable to find first-hop BR for path", nil, "ifID", ifID)
	}
	return &snet.Addr{
		IA:      ps.FirstIA(),
		Host:    addr.NewSVCUDPAppAddr(svc),
		Path:    p,
		NextHop: overlayNextHop,
	}, nil
}
