// Copyright 2016 ETH Zurich
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

package topology

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/scionproto/scion/go/lib/addr"
	"github.com/scionproto/scion/go/lib/common"
	"github.com/scionproto/scion/go/lib/topology/overlay"
	"github.com/scionproto/scion/go/proto"
)

var testTopo *Topo

func mkO(l3 addr.HostAddr, op int) *overlay.OverlayAddr {
	if l3 == nil {
		return nil
	}
	return overlay.NewOverlayAddr(l3.IP(), uint16(op))
}

func mkPBO(ip string, port int, bindip string, bindport int, op int) *pubBindAddr {
	pub := addr.HostFromIPStr(ip)
	pbo := &pubBindAddr{}
	pbo.pub = &addr.AppAddr{L3: pub}
	if port != 0 {
		pbo.pub.L4 = uint16(port)
	}
	if bindip != "" {
		pbo.bind = &addr.AppAddr{L3: addr.HostFromIPStr(bindip)}
		if bindport != 0 {
			pbo.bind.L4 = uint16(bindport)
		}
	}
	pbo.overlay = mkO(pub, op)
	return pbo
}

func mkOB(ip string, port int, bindip string) *OverBindAddr {
	overlay := addr.HostFromIPStr(ip)
	var bind addr.HostAddr
	if bindip != "" {
		bind = addr.HostFromIPStr(bindip)
	}
	return &OverBindAddr{
		PublicOverlay: mkO(overlay, port),
		BindOverlay:   mkO(bind, port),
	}
}

func mkTAv4(ip string, port int, bindip string, bindport int, ot overlay.Type, op int) TopoAddr {
	return TopoAddr{IPv4: mkPBO(ip, port, bindip, bindport, op), Overlay: ot}
}

func mkTAv6(ip string, port int, bindip string, bindport int, ot overlay.Type, op int) TopoAddr {
	return TopoAddr{IPv6: mkPBO(ip, port, bindip, bindport, op), Overlay: ot}
}

func loadTopo(filename string, t *testing.T) {
	topo, err := LoadFromFile(filename)
	require.NoError(t, err, "Error loading config from '%s': %v", filename, err)
	testTopo = topo
}

func TestMeta(t *testing.T) {
	fn := "testdata/basic.json"
	loadTopo(fn, t)
	c := testTopo
	assert.Equal(t, time.Unix(168570123, 0), c.Timestamp, "Field 'Timestamp'")
	// Is testing this piece of data really useful?
	assert.Contains(t, c.TimestampHuman, "1975-05-06 01:02:03.000000+0000",
		"Field 'TimestampHuman'")
	assert.Equal(t, time.Hour, c.TTL, "Field 'TTL'")
	assert.Equal(t, addr.IA{I: 1, A: 0xff0000000311}, c.ISD_AS, "Field 'ISD_AS'")
	assert.Equal(t, overlay.IPv46, c.Overlay, "Field 'Overlay'")
	assert.Equal(t, 1472, c.MTU, "Field 'MTU'")
	assert.False(t, c.Core, "Field 'Core'")
}

func Test_Active(t *testing.T) {
	fn := "testdata/basic.json"
	Convey("Checking Active", t, func() {
		loadTopo(fn, t)
		c := testTopo
		Convey("Given a positive TTL", func() {
			SoMsg("Before TS inactive", c.Active(c.Timestamp.Add(-time.Second)), ShouldBeFalse)
			SoMsg("TS in active range", c.Active(c.Timestamp), ShouldBeTrue)
			SoMsg("End of active range", c.Active(c.Timestamp.Add(c.TTL-1)), ShouldBeTrue)
			SoMsg("Expired inactive", c.Active(c.Timestamp.Add(time.Hour)), ShouldBeFalse)
		})
		Convey("Given a zero TTL", func() {
			c.TTL = 0
			SoMsg("Before TS inactive", c.Active(c.Timestamp.Add(-time.Second)), ShouldBeFalse)
			SoMsg("TS in active range", c.Active(c.Timestamp), ShouldBeTrue)
			SoMsg("Distant time active", c.Active(c.Timestamp.Add(100*time.Hour)), ShouldBeTrue)
		})
	})
}

func Test_BRs(t *testing.T) {
	brs := map[string]BRInfo{
		"br1-ff00:0:311-1": {
			IFIDs: []common.IFIDType{1, 3, 8},
		},
	}
	brn := []string{"br1-ff00:0:311-1"}

	fn := "testdata/basic.json"
	loadTopo(fn, t)
	c := testTopo
	for name, info := range brs {
		Convey(fmt.Sprintf("Checking BR details for %s", name), t, func() {
			So(c.BR, ShouldContainKey, name)
			for _, i := range info.IFIDs {
				Convey(fmt.Sprintf("Checking if %s has interface with id %v", name, i), func() {
					So(c.BR[name].IFIDs, ShouldContain, i)
				})
			}
			So(c.BRNames, ShouldResemble, brn)
		})
	}
	Convey("Checking that BR map has no extra entries ", t, func() {
		So(len(c.BR), ShouldEqual, len(brn))
	})
}

func TestServiceDetails(t *testing.T) {
	fn := "testdata/basic.json"
	ot := overlay.IPv46
	// We do this just for CSs since the code for the other non-BR
	cses := IDAddrMap{
		// v4 with bind
		"cs1-ff00:0:311-1": mkTAv4("127.0.0.66", 30081, "127.0.0.67", 30081, ot, 0),
		// v4 without bind
		"cs1-ff00:0:311-2": mkTAv4("127.0.0.67", 30073, "", 0, ot, 0),
		// v6 without bind
		"cs1-ff00:0:311-3": mkTAv6("2001:db8:f00:b43::1", 23421, "", 0, ot, 0),
		// v6 with bind
		"cs1-ff00:0:311-4": mkTAv6("2001:db8:f00:b43::2", 23421, "2001:db8:1714::1", 13373, ot, 0),
	}
	loadTopo(fn, t)
	c := testTopo
	assert.Equal(t, cses, c.CS)
}

func TestServiceCount(t *testing.T) {
	// This just checks the count of all the service types, actual population
	// testing is done elsewhere
	// The simple counting check for CS is done in the detailed population test as well
	fn := "testdata/basic.json"
	loadTopo(fn, t)
	c := testTopo
	assert.Len(t, c.BS, 3, "BS")
	assert.Len(t, c.PS, 2, "PS")
	assert.Len(t, c.SB, 2, "SB")
	assert.Len(t, c.RS, 2, "RS")
	assert.Len(t, c.SIG, 2, "SIG")
	assert.Len(t, c.DS, 2, "DS")
}

func TestIFInfoMap(t *testing.T) {
	ifm := make(IfInfoMap)
	isdas, _ := addr.IAFromString("1-ff00:0:312")
	ifm[1] = IFInfo{
		Id:     1,
		BRName: "br1-ff00:0:311-1",
		InternalAddrs: &TopoBRAddr{
			IPv4:    mkOB("10.1.0.1", 0, ""),
			IPv6:    mkOB("2001:db8:a0b:12f0::1", 0, ""),
			Overlay: overlay.IPv46},
		CtrlAddrs: &TopoAddr{
			IPv4:    mkPBO("10.1.0.1", 30098, "", 0, 0),
			IPv6:    mkPBO("2001:db8:a0b:12f0::1", 30098, "", 0, 0),
			Overlay: overlay.IPv46},
		Overlay: overlay.UDPIPv4,
		Local: &TopoBRAddr{
			IPv4:    mkOB("192.0.2.1", 44997, "10.0.0.1"),
			Overlay: overlay.UDPIPv4},
		Remote:    mkO(addr.HostFromIPStr("192.0.2.2"), 44998),
		Bandwidth: 1000,
		ISD_AS:    isdas,
		LinkType:  proto.LinkType_parent,
		MTU:       1472,
	}
	isdas, _ = addr.IAFromString("1-ff00:0:314")
	ifm[3] = IFInfo{
		Id:     3,
		BRName: "br1-ff00:0:311-1",
		InternalAddrs: &TopoBRAddr{
			IPv4:    mkOB("10.1.0.1", 0, ""),
			IPv6:    mkOB("2001:db8:a0b:12f0::1", 0, ""),
			Overlay: overlay.IPv46},
		CtrlAddrs: &TopoAddr{
			IPv4:    mkPBO("10.1.0.1", 30098, "", 0, 0),
			IPv6:    mkPBO("2001:db8:a0b:12f0::1", 30098, "", 0, 0),
			Overlay: overlay.IPv46},
		Overlay: overlay.IPv6,
		Local: &TopoBRAddr{
			IPv6:    mkOB("2001:db8:a0b:12f0::1", 0, "2001:db8:a0b:12f0::8"),
			Overlay: overlay.IPv6},
		Remote:    mkO(addr.HostFromIPStr("2001:db8:a0b:12f0::2"), 0),
		Bandwidth: 5000,
		ISD_AS:    isdas,
		LinkType:  proto.LinkType_child,
		MTU:       4430,
	}
	isdas, _ = addr.IAFromString("1-ff00:0:313")
	ifm[8] = IFInfo{
		Id:     8,
		BRName: "br1-ff00:0:311-1",
		InternalAddrs: &TopoBRAddr{
			IPv4:    mkOB("10.1.0.1", 0, ""),
			IPv6:    mkOB("2001:db8:a0b:12f0::1", 0, ""),
			Overlay: overlay.IPv46},
		CtrlAddrs: &TopoAddr{
			IPv4:    mkPBO("10.1.0.1", 30098, "", 0, 0),
			IPv6:    mkPBO("2001:db8:a0b:12f0::1", 30098, "", 0, 0),
			Overlay: overlay.IPv46},
		Overlay: overlay.IPv4,
		Local: &TopoBRAddr{
			IPv4:    mkOB("192.0.2.2", 0, "10.0.0.2"),
			Overlay: overlay.IPv4},
		Remote:    mkO(addr.HostFromIPStr("192.0.2.3"), 0),
		Bandwidth: 2000,
		ISD_AS:    isdas,
		LinkType:  proto.LinkType_peer,
		MTU:       1480,
	}
	fn := "testdata/basic.json"
	loadTopo(fn, t)
	assert.Equal(t, ifm, testTopo.IFInfoMap)
}

func TestIFInfoMapCoreAS(t *testing.T) {
	ifm := make(IfInfoMap)
	isdas, _ := addr.IAFromString("6-ff00:0:363")
	ifm[91] = IFInfo{
		Id:     91,
		BRName: "borderrouter6-ff00:0:362-1",
		InternalAddrs: &TopoBRAddr{
			IPv4:    mkOB("10.1.0.1", 0, ""),
			IPv6:    mkOB("2001:db8:a0b:12f0::1", 0, ""),
			Overlay: overlay.IPv46},
		CtrlAddrs: &TopoAddr{
			IPv4:    mkPBO("10.1.0.1", 30098, "", 0, 0),
			IPv6:    mkPBO("2001:db8:a0b:12f0::1", 30098, "", 0, 0),
			Overlay: overlay.IPv46},
		Overlay: overlay.UDPIPv4,
		Local: &TopoBRAddr{
			IPv4:    mkOB("192.0.2.1", 4997, "10.0.0.1"),
			Overlay: overlay.UDPIPv4},
		Remote:    mkO(addr.HostFromIPStr("192.0.2.2"), 4998),
		Bandwidth: 100000,
		ISD_AS:    isdas,
		LinkType:  proto.LinkType_core,
		MTU:       1472,
	}
	isdas, _ = addr.IAFromString("6-ff00:0:364")
	ifm[32] = IFInfo{
		Id:     32,
		BRName: "borderrouter6-ff00:0:362-9",
		InternalAddrs: &TopoBRAddr{
			IPv4:    mkOB("10.1.0.2", 0, ""),
			IPv6:    mkOB("2001:db8:a0b:12f0::2", 0, ""),
			Overlay: overlay.IPv46},
		CtrlAddrs: &TopoAddr{
			IPv4:    mkPBO("10.1.0.2", 3098, "", 0, 0),
			IPv6:    mkPBO("2001:db8:a0b:12f0::2", 3098, "", 0, 0),
			Overlay: overlay.IPv46},
		Overlay: overlay.IPv6,
		Local: &TopoBRAddr{
			IPv6:    mkOB("2001:db8:a0b:12f0::1", 0, "2001:db8:a0b:12f0::8"),
			Overlay: overlay.IPv6},
		Remote:    mkO(addr.HostFromIPStr("2001:db8:a0b:12f0::2"), 0),
		Bandwidth: 5000,
		ISD_AS:    isdas,
		LinkType:  proto.LinkType_child,
		MTU:       4430,
	}
	fn := "testdata/core.json"
	loadTopo(fn, t)
	assert.Equal(t, ifm, testTopo.IFInfoMap)
}

func TestBRsCoreAS(t *testing.T) {
	brCases := []struct {
		name    string
		intfids []common.IFIDType
	}{
		{name: "borderrouter6-ff00:0:362-1", intfids: []common.IFIDType{91}},
		{name: "borderrouter6-ff00:0:362-9", intfids: []common.IFIDType{32}},
	}
	fn := "testdata/core.json"
	loadTopo(fn, t)
	c := testTopo
	for _, test := range brCases {
		Convey(fmt.Sprintf("Checking BR details for %s", test.name), t, func() {
			Convey(fmt.Sprintf("Checking whether topo has a BR named %s", test.name), func() {
				So(c.BR, ShouldContainKey, test.name)
			})
			for _, i := range test.intfids {
				Convey(fmt.Sprintf("Checking if %s has interface with id %v", test.name, i),
					func() {
						So(c.BR[test.name].IFIDs, ShouldContain, i)
					})
			}
		})
	}
	Convey("Checking if the number of BRs in the Topo is correct", t, func() {
		So(len(c.BR), ShouldEqual, len(brCases))
	})
}

func TestTopoFromStripped(t *testing.T) {
	fn := "testdata/basic.json"
	rt, err := LoadRawFromFile(fn)
	require.NoError(t, err, "Error loading raw topo from '%s': %v", fn, err)
	Convey("Check that stripped bind topology can be parsed", t, func() {
		StripBind(rt)
		b, err := json.Marshal(rt)
		SoMsg("errPack", err, ShouldBeNil)
		_, err = Load(b)
		SoMsg("errParse", err, ShouldBeNil)
	})
	Convey("Check that stripped svc topology can be parsed", t, func() {
		StripServices(rt)
		b, err := json.Marshal(rt)
		SoMsg("errPack", err, ShouldBeNil)
		_, err = Load(b)
		SoMsg("errParse", err, ShouldBeNil)
	})
	Convey("Check that stripped topology can be parsed", t, func() {
		StripBind(rt)
		StripServices(rt)
		b, err := json.Marshal(rt)
		SoMsg("errPack", err, ShouldBeNil)
		_, err = Load(b)
		SoMsg("errParse", err, ShouldBeNil)
	})
}
