// Copyright 2016 ETH Zurich
// Copyright 2019 ETH Zurich, Anapaya Systems
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
	"fmt"
	"net"

	"github.com/scionproto/scion/go/border/brconf"
	"github.com/scionproto/scion/go/border/rcmn"
	"github.com/scionproto/scion/go/border/rctx"
	"github.com/scionproto/scion/go/lib/common"
	"github.com/scionproto/scion/go/lib/log"
	"github.com/scionproto/scion/go/lib/overlay/conn"
	"github.com/scionproto/scion/go/lib/ringbuf"
	"github.com/scionproto/scion/go/lib/topology"
)

const PosixSock brconf.SockType = "posix"

func init() {
	registeredLocSockOps[PosixSock] = posixLoc{}
	registeredExtSockOps[PosixSock] = posixExt{}
}

var _ locSockOps = posixLoc{}

type posixLoc struct{}

// Setup configures a local POSIX(/BSD) socket.
func (p posixLoc) Setup(r *Router, ctx *rctx.Ctx, oldCtx *rctx.Ctx) error {

	// Check if the existing socket can be reused. On startup, oldCtx is nil.
	if oldCtx != nil {
		// The socket can be reused if the local address does not change.
		if ctx.Conf.BR.InternalAddrs.Equal(oldCtx.Conf.BR.InternalAddrs) {
			log.Trace("No change detected for local socket.")
			// Nothing changed. Copy I/O functions from old context.
			ctx.LocSockIn = oldCtx.LocSockIn
			ctx.LocSockOut = oldCtx.LocSockOut
			return nil
		}
		log.Debug("Closing existing local socket", "conn", oldCtx.LocSockIn.Conn.LocalAddr())
		oldCtx.LocSockIn.Stop()
		oldCtx.LocSockOut.Stop()
	}
	// New bind address. Configure Posix I/O.
	return p.addSock(r, ctx)
}

func (p posixLoc) Rollback(r *Router, ctx *rctx.Ctx, oldCtx *rctx.Ctx) error {

	// Do nothing if socket is reused.
	if oldCtx != nil && ctx.Conf.BR.InternalAddrs.Equal(oldCtx.Conf.BR.InternalAddrs) {
		return nil
	}
	// Remove new socket if it exists. It might not be set if the setup failed.
	if ctx.LocSockIn != nil {
		log.Debug("Rolling back local socket", "conn", ctx.LocSockIn.Conn.LocalAddr)
		ctx.LocSockIn.Stop()
		ctx.LocSockOut.Stop()
	}
	// No need to start socket if the old context unset or the socket is still running.
	if oldCtx == nil || oldCtx.LocSockIn.Running() {
		return nil
	}
	// Replace previously closed socket.
	return p.addSock(r, oldCtx)
}

func (p posixLoc) addSock(r *Router, ctx *rctx.Ctx) error {
	// Get Bind address if set, Public otherwise
	bindOv := ctx.Conf.BR.InternalAddrs.BindOrPublicOverlay(ctx.Conf.Topo.Overlay())
	var bind *net.UDPAddr
	if bindOv != nil {
		bind = bindOv.ToUDPAddr()
	}
	log.Debug("Setting up new local socket.", "bind", bind)
	// Listen on the socket.
	over, err := conn.New(bind, nil, nil)
	if err != nil {
		return common.NewBasicError("Unable to listen on local socket", err, "bind", bind)
	}
	// Setup input goroutine.
	ctx.LocSockIn = rctx.NewSock(ringbuf.New(64, nil, "loc_in"),
		over, rcmn.DirLocal, 0, "", r.posixInput, r.handleSock, PosixSock)
	ctx.LocSockOut = rctx.NewSock(ringbuf.New(64, nil, "loc_out"),
		over, rcmn.DirLocal, 0, "", nil, r.posixOutput, PosixSock)
	log.Debug("Done setting up new local socket.", "conn", over.LocalAddr())
	return nil
}

var _ extSockOps = posixExt{}

type posixExt posixLoc

// Setup configures a POSIX(/BSD) interface socket.
func (p posixExt) Setup(r *Router, ctx *rctx.Ctx, intf *topology.IFInfo, oldCtx *rctx.Ctx) error {
	// No old context. This happens during startup of the router.
	if oldCtx == nil {
		return p.addIntf(r, ctx, intf)
	}
	if oldIntf, ok := oldCtx.Conf.BR.IFs[intf.Id]; ok {
		// Reuse socket if the interface has not changed.
		if !interfaceChanged(intf, oldIntf) {
			log.Trace("No change detected for external socket.", "conn",
				intf.Local.BindOrPublicOverlay(ctx.Conf.Topo.Overlay()))
			ctx.ExtSockIn[intf.Id] = oldCtx.ExtSockIn[intf.Id]
			ctx.ExtSockOut[intf.Id] = oldCtx.ExtSockOut[intf.Id]
			return nil
		}
		log.Debug("Closing existing external socket", "old", oldIntf, "new", intf)
		oldCtx.ExtSockIn[intf.Id].Stop()
		oldCtx.ExtSockOut[intf.Id].Stop()
	}
	return p.addIntf(r, ctx, intf)
}

func (p posixExt) Rollback(r *Router, ctx *rctx.Ctx, intf *topology.IFInfo,
	oldCtx *rctx.Ctx) error {

	var oldIntf *topology.IFInfo
	if oldCtx != nil {
		oldIntf = oldCtx.Conf.BR.IFs[intf.Id]
	}
	// Do not rollback socket if it is reused by new context.
	if oldIntf != nil && !interfaceChanged(intf, oldIntf) {
		return nil
	}
	// Stop new socket if it exists. It might not exist if the Setup failed.
	if _, ok := ctx.ExtSockIn[intf.Id]; ok {
		log.Debug("Rolling back external socket", "intf", intf)
		ctx.ExtSockIn[intf.Id].Stop()
		ctx.ExtSockOut[intf.Id].Stop()
	}
	// No need to start socket if it is not present in old context or still running.
	// The socket is still running if setupNet failed before iterating over this socket.
	if oldIntf == nil || oldCtx.ExtSockIn[oldIntf.Id].Running() {
		return nil
	}
	return p.addIntf(r, oldCtx, oldIntf)
}

func (p posixExt) addIntf(r *Router, ctx *rctx.Ctx, intf *topology.IFInfo) error {

	// Connect to remote address.
	log.Debug("Setting up new external socket.", "intf", intf)
	bindOv := intf.Local.BindOrPublicOverlay(intf.Local.Overlay)
	var bind *net.UDPAddr
	if bindOv != nil {
		bind = bindOv.ToUDPAddr()
	}
	remoteOv := intf.Remote
	var remote *net.UDPAddr
	if remoteOv != nil {
		remote = remoteOv.ToUDPAddr()
	}
	c, err := conn.New(bind, remote, nil)
	if err != nil {
		return common.NewBasicError("Unable to listen on external socket", err)
	}
	// Setup input goroutine.
	ctx.ExtSockIn[intf.Id] = rctx.NewSock(
		ringbuf.New(64, nil, fmt.Sprintf("ext_in_%s", intf.Id)),
		c, rcmn.DirExternal, intf.Id, intf.ISD_AS.String(), r.posixInput, r.handleSock, PosixSock)
	ctx.ExtSockOut[intf.Id] = rctx.NewSock(
		ringbuf.New(64, nil, fmt.Sprintf("ext_out_%s", intf.Id)),
		c, rcmn.DirExternal, intf.Id, intf.ISD_AS.String(), nil, r.posixOutput, PosixSock)
	log.Debug("Done setting up new external socket.", "intf", intf)
	return nil
}

func (p posixExt) Teardown(r *Router, ctx *rctx.Ctx, intf *topology.IFInfo, oldCtx *rctx.Ctx) {
	if oldCtx == nil || oldCtx.ExtSockIn[intf.Id] == nil {
		return
	}
	if _, ok := ctx.ExtSockIn[intf.Id]; !ok {
		log.Debug("Tearing down socket from removed external interface", "intf", intf)
		oldCtx.ExtSockIn[intf.Id].Stop()
		oldCtx.ExtSockOut[intf.Id].Stop()
	}
}

// interfaceChanged returns true if a new input goroutine is needed for the
// corresponding interface.
func interfaceChanged(newIntf *topology.IFInfo, oldIntf *topology.IFInfo) bool {
	return (newIntf.Id != oldIntf.Id ||
		!newIntf.Local.Equal(oldIntf.Local) ||
		!newIntf.Remote.Equal(oldIntf.Remote))
}
