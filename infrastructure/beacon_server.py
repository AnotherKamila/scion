# Copyright 2014 ETH Zurich

# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at

# http://www.apache.org/licenses/LICENSE-2.0

# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
"""
:mod:`beacon_server` --- SCION beacon server
===========================================
"""

from _collections import deque
from infrastructure.scion_elem import SCIONElement
from lib.packet.host_addr import IPv4HostAddr
from lib.packet.opaque_field import (OpaqueFieldType as OFT, InfoOpaqueField,
    SupportSignatureField, HopOpaqueField, SupportPCBField, SupportPeerField,
    ROTField)
from lib.packet.pcb import (PathSegment, ADMarking, PCBMarking, PeerMarking,
    PathConstructionBeacon, PathSegmentInfo, PathSegmentRecords,
    PathSegmentType as PST)
from lib.packet.scion import (SCIONPacket, get_type, PacketType as PT,
    CertRequest, TRCRequest, CertReply, TRCReply)
from lib.crypto.certificate import sign, verify, CertificateChain, TRC
from lib.util import (read_file, write_file, get_cert_file_path,
    get_sig_key_file_path, get_trc_file_path)
import logging
import sys
import threading
import time
import os
import copy
import base64


class BeaconServer(SCIONElement):
    """
    The SCION PathConstructionBeacon Server.

    Attributes:
        beacons: A FIFO queue containing the beacons for processing and
            propagation.
        reg_queue: A FIFO queue containing paths for registration with path
            servers.
    """
    DELTA = 24 * 60 * 60  # Amount of real time a PCB packet is valid for.
    TIME_INTERVAL = 4  # SCION second
    BEACONS_NO = 5
    REGISTERED_PATHS = 100

    def __init__(self, addr, topo_file, config_file):
        SCIONElement.__init__(self, addr, topo_file, config_file)
        self.beacons = deque()
        self.reg_queue = deque()
        sig_key_file = get_sig_key_file_path(self.topology.isd_id,
            self.topology.ad_id, 0)
        self.signing_key = read_file(sig_key_file)
        self.signing_key = base64.b64decode(self.signing_key)

    def propagate_downstream_pcb(self, pcb):
        """
        Propagates the beacon to all children.
        """
        assert isinstance(pcb, PathSegment)
        ingress_if = pcb.rotf.if_id
        for router_child in self.topology.child_edge_routers:
            new_pcb = copy.deepcopy(pcb)
            egress_if = router_child.interface.if_id
            new_pcb.rotf.if_id = egress_if
            ad_marking = self._create_ad_marking(ingress_if, egress_if)
            new_pcb.add_ad(ad_marking)
            beacon = PathConstructionBeacon.from_values(router_child.addr,
                                                        new_pcb)
            self.send(beacon, router_child.addr)
            logging.info("Downstream PCB propagated!")

    def handle_pcbs_propagation(self):
        """
        Main loop to propagate received beacons.
        """
        while True:
            while self.beacons:
                pcb = self.beacons.popleft()
                self.propagate_downstream_pcb(pcb)
                self.reg_queue.append(pcb)
            time.sleep(self.config.propagation_time)

    def process_pcb(self, beacon):
        """
        Receives beacon and appends it to beacon list.
        """
        pass

    def register_segments(self):
        """
        Registers paths according to the received beacons.
        """
        pass

    def _create_ad_marking(self, ingress_if, egress_if):
        """
        Creates an AD Marking with the given ingress and egress interfaces.
        """
        ssf = SupportSignatureField()
        hof = HopOpaqueField.from_values(ingress_if, egress_if)
        spcbf = SupportPCBField.from_values(isd_id=self.topology.isd_id)
        pcbm = PCBMarking.from_values(self.topology.ad_id, ssf, hof, spcbf)
        data_to_sign = str(pcbm.ad_id) + str(pcbm.hof) + str(pcbm.spcbf)
        peer_markings = []
        # TODO PSz: peering link can be only added when there is
        # IfidReply from router
        for router_peer in self.topology.peer_edge_routers:
            hof = HopOpaqueField.from_values(router_peer.interface.if_id,
                                             egress_if)
            spf = SupportPeerField.from_values(self.topology.isd_id)
            peer_marking = \
                PeerMarking.from_values(router_peer.interface.neighbor_ad,
                                        hof, spf)
            data_to_sign += str(peer_marking)
            peer_markings.append(peer_marking)
        signature = sign(data_to_sign, self.signing_key)
        return ADMarking.from_values(pcbm, peer_markings, signature)

    def handle_request(self, packet, sender, from_local_socket=True):
        """
        Main routine to handle incoming SCION packets.
        """
        spkt = SCIONPacket(packet)
        ptype = get_type(spkt)
        if ptype == PT.IFID_REQ:
            # TODO
            logging.warning("IFID_REQ received, to implement")
        elif ptype == PT.IFID_REP:
            # TODO
            logging.warning("IFID_REP received, to implement")
        elif ptype == PT.BEACON:
            self.process_pcb(PathConstructionBeacon(packet))
        elif ptype == PT.CERT_REP:
            self.process_cert_rep(CertReply(packet))
        elif ptype == PT.TRC_REP:
            self.process_trc_rep(TRCReply(packet))
        else:
            logging.warning("Type not supported")

    def run(self):
        """
        Run an instance of the Beacon Server.
        """
        threading.Thread(target=self.handle_pcbs_propagation).start()
        threading.Thread(target=self.register_segments).start()
        SCIONElement.run(self)


class CoreBeaconServer(BeaconServer):
    """
    PathConstructionBeacon Server in a core AD.

    Starts broadcasting beacons down-stream within an ISD and across ISDs
    towards other core beacon servers.
    """
    def __init__(self, addr, topo_file, config_file):
        BeaconServer.__init__(self, addr, topo_file, config_file)
        # Sanity check that we should indeed be a core beacon server.
        assert self.topology.is_core_ad, "This shouldn't be a core BS!"

    def propagate_core_pcb(self, pcb):
        """
        Propagates the core beacons to other core ADs.
        """
        assert isinstance(pcb, PathSegment)
        ingress_if = pcb.rotf.if_id
        for core_router in self.topology.routing_edge_routers:
            new_pcb = copy.deepcopy(pcb)
            egress_if = core_router.interface.if_id
            new_pcb.rotf.if_id = egress_if
            ad_marking = self._create_ad_marking(ingress_if, egress_if)
            new_pcb.add_ad(ad_marking)
            beacon = PathConstructionBeacon.from_values(core_router.addr,
                                                        new_pcb)
            self.send(beacon, core_router.addr)
            logging.info("Core PCB propagated!")

    def handle_pcbs_propagation(self):
        """
        Generates a new beacon or gets ready to forward the one received.
        """
        while True:
            # Create beacon for downstream ADs.
            downstream_pcb = PathSegment()
            timestamp = (((int(time.time()) + BeaconServer.DELTA) %
                          (BeaconServer.TIME_INTERVAL * (2 ** 16))) /
                         BeaconServer.TIME_INTERVAL)
            downstream_pcb.iof = InfoOpaqueField.from_values(OFT.TDC_XOVR,
                False, timestamp, self.topology.isd_id)
            downstream_pcb.rotf = ROTField()
            self.propagate_downstream_pcb(downstream_pcb)
            # Create beacon for core ADs.
            core_pcb = PathSegment()
            core_pcb.iof = InfoOpaqueField.from_values(OFT.TDC_XOVR, False,
                                                       timestamp,
                                                       self.topology.isd_id)
            core_pcb.rotf = ROTField()
            self.propagate_core_pcb(core_pcb)
            # Propagate received beacons. A core beacon server can only receive
            # beacons from other core beacon servers.
            while self.beacons:
                pcb = self.beacons.popleft()
                self.propagate_core_pcb(pcb)
                self.reg_queue.append(pcb)
            time.sleep(self.config.propagation_time)

    def register_segments(self):
        if not self.config.registers_paths:
            logging.info("Path registration unwanted, leaving"
                         "register_segments")
            return
        while True:
            while self.reg_queue:
                pcb = self.reg_queue.popleft()
                new_pcb = copy.deepcopy(pcb)
                ad_marking = self._create_ad_marking(new_pcb.rotf.if_id, 0)
                new_pcb.add_ad(ad_marking)
                self.register_core_segment(new_pcb)
                logging.info("Paths registered")
            time.sleep(self.config.registration_time)

    def register_core_segment(self, pcb):
        """
        Registers the core segment contained in 'pcb' with the local core path
        server and the originating core path server.
        """
        info = PathSegmentInfo.from_values(PST.CORE,
                                           pcb.get_first_pcbm().spcbf.isd_id,
                                           self.topology.isd_id,
                                           pcb.get_first_pcbm().ad_id,
                                           self.topology.ad_id)
        pcb.remove_signatures()
        # Register core path with local core path server.
        if self.topology.path_servers != []:
            # TODO: pick other than the first path server
            dst = self.topology.path_servers[0].addr
            path_rec = PathSegmentRecords.from_values(dst, info, [pcb])
            logging.debug("Registering core path with local PS.")
            self.send(path_rec, dst)
        # Register core path with originating core path server.
        path = pcb.get_path(reverse_direction=True)
        path_rec = PathSegmentRecords.from_values(self.addr, info, [pcb], path)
        if_id = path.get_first_hop_of().ingress_if
        next_hop = self.ifid2addr[if_id]
        logging.debug("Registering core path with originating PS.")
        self.send(path_rec, next_hop)

    def process_pcb(self, beacon):
        assert isinstance(beacon, PathConstructionBeacon)
        logging.info("PCB received")
        pcb = beacon.pcb
        # Before we append the PCB for further processing we need to check that
        # it hasn't been received before.
        for ad in pcb.ads:
            isd_id = ad.pcbm.spcbf.isd_id
            ad_id = ad.pcbm.ad_id
            if (isd_id == self.topology.isd_id and
                ad_id == self.topology.ad_id):
                logging.debug("Core Segment PCB already seen. Dropping...")
                return
        self.beacons.append(pcb)


class LocalBeaconServer(BeaconServer):
    """
    PathConstructionBeacon Server in a non-core AD.

    Receives, processes, and propagates beacons received by other beacon
    servers.
    """
    REQUESTS_TIMEOUT = 10

    def __init__(self, addr, topo_file, config_file):
        BeaconServer.__init__(self, addr, topo_file, config_file)
        # Sanity check that we should indeed be a local beacon server.
        assert not self.topology.is_core_ad, "This shouldn't be a local BS!"
        self.unverified_beacons = deque()
        self.registered_beacons = []
        self.requested_certs = {}
        self.requested_trcs = {}

    def register_up_segment(self, pcb):
        """
        Send up-segment to Local Path Servers
        """
        info = PathSegmentInfo.from_values(PST.UP,
                                           self.topology.isd_id,
                                           self.topology.isd_id,
                                           pcb.get_first_pcbm().ad_id,
                                           self.topology.ad_id)
        # TODO: pick other than the first path server
        dst = self.topology.path_servers[0].addr
        up_path = PathSegmentRecords.from_values(dst, info, [pcb])
        self.send(up_path, dst)

    def register_down_segment(self, pcb):
        """
        Send down-segment to Core Path Server
        """
        info = PathSegmentInfo.from_values(PST.DOWN,
                                           self.topology.isd_id,
                                           self.topology.isd_id,
                                           pcb.get_first_pcbm().ad_id,
                                           self.topology.ad_id)
        core_path = pcb.get_path(reverse_direction=True)
        down_path = PathSegmentRecords.from_values(self.addr, info, [pcb],
                                                   core_path)
        if_id = core_path.get_first_hop_of().ingress_if
        next_hop = self.ifid2addr[if_id]
        self.send(down_path, next_hop)

    def register_segments(self):
        """
        Registers paths according to the received beacons.
        """
        if not self.config.registers_paths:
            logging.info("Path registration unwanted, "
                         "leaving register_segments")
            return
        while True:
            while self.reg_queue:
                pcb = self.reg_queue.popleft()
                new_pcb = copy.deepcopy(pcb)
                ad_marking = self._create_ad_marking(new_pcb.rotf.if_id, 0)
                new_pcb.add_ad(ad_marking)
                new_pcb.remove_signatures()
                self.register_up_segment(new_pcb)
                self.register_down_segment(new_pcb)
                logging.info("Paths registered")
            time.sleep(self.config.registration_time)

    def process_pcb(self, beacon):
        """
        Receives beacon and appends it to beacon list.
        """
        assert isinstance(beacon, PathConstructionBeacon)
        logging.info("PCB received")
        if self._is_beacon_registered(beacon.pcb):
            logging.debug("Beacon already seen before.")
            self.beacons.append(beacon.pcb)
        else:
            logging.debug("Beacon never seen before.")
            cert_isd = beacon.pcb.get_last_pcbm().spcbf.isd_id
            cert_ad = beacon.pcb.get_last_pcbm().ad_id
            trc_version = beacon.pcb.rotf.rot_version
            if self._check_certs_trc(cert_isd, cert_ad, trc_version,
                beacon.pcb.rotf.if_id):
                if self._verify_beacon(beacon.pcb):
                    self.registered_beacons.append(beacon.pcb)
                    self.beacons.append(beacon.pcb)
                    logging.info("Registered valid beacon.")
                else:
                    logging.info("Invalid beacon.")
                    return
            else:
                logging.debug("Certificate(s) or TRC missing.")
                self.unverified_beacons.append(beacon.pcb)

    def _is_beacon_registered(self, pcb):
        """
        Return True or False whether a beacon was previously registered.
        """
        assert isinstance(pcb, PathSegment)
        for reg_pcb in self.registered_beacons:
            if reg_pcb.compare_hops(pcb):
                return True
        return False

    def _check_certs_trc(self, isd_id, cert_ad, trc_version, if_id):
        """
        Return True or False whether the necessary Certificate and TRC files are
        found.
        """
        trc_file = get_trc_file_path(self.topology.isd_id, self.topology.ad_id,
            isd_id, trc_version)
        if os.path.exists(trc_file):
            trc = TRC(trc_file)
            cert_file = get_cert_file_path(self.topology.isd_id,
                self.topology.ad_id, isd_id, cert_ad, 0)
            my_cert_file = get_cert_file_path(self.topology.isd_id,
                self.topology.ad_id, self.topology.isd_id, self.topology.ad_id,
                0)
            my_cert = CertificateChain(my_cert_file)
            if (os.path.exists(cert_file) or
                my_cert.certs[0].issuer in trc.core_ads):
                return True
            else:
                cert_tuple = (isd_id, cert_ad, 0)
                now = int(time.time())
                if cert_tuple not in self.requested_certs :
                    new_cert_req = CertRequest.from_values(PT.CERT_REQ_LOCAL,
                        self.addr, if_id, self.topology.isd_id,
                        self.topology.ad_id, isd_id, cert_ad, 0)
                    dst_addr = self.topology.certificate_servers[0].addr
                    self.send(new_cert_req, dst_addr)
                    self.requested_certs[cert_tuple] = now
                    return False
                if (now - self.requested_certs[cert_tuple] >
                    LocalBeaconServer.REQUESTS_TIMEOUT):
                    new_cert_req = CertRequest.from_values(PT.CERT_REQ_LOCAL,
                        self.addr, if_id, self.topology.isd_id,
                        self.topology.ad_id, isd_id, cert_ad, 0)
                    dst_addr = self.topology.certificate_servers[0].addr
                    self.send(new_cert_req, dst_addr)
                    self.requested_certs[cert_tuple] = now
                    return False
        else:
            trc_tuple = (isd_id, trc_version)
            now = int(time.time())
            if trc_tuple not in self.requested_trcs:
                new_trc_req = TRCRequest.from_values(PT.TRC_REQ_LOCAL,
                    self.addr, if_id, self.topology.isd_id, self.topology.ad_id,
                    isd_id, trc_version)
                dst_addr = self.topology.certificate_servers[0].addr
                self.send(new_trc_req, dst_addr)
                self.requested_trcs[trc_tuple] = now
                return False
            if (now - self.requested_trcs[trc_tuple] >
                LocalBeaconServer.REQUESTS_TIMEOUT):
                new_trc_req = TRCRequest.from_values(PT.TRC_REQ_LOCAL,
                    self.addr, if_id, self.topology.isd_id, self.topology.ad_id,
                    isd_id, trc_version)
                dst_addr = self.topology.certificate_servers[0].addr
                self.send(new_trc_req, dst_addr)
                self.requested_trcs[trc_tuple] = now
                return False

    def process_cert_rep(self, cert_rep):
        """
        Process the Certificate reply.
        """
        assert isinstance(cert_rep, CertReply)
        logging.info("Certificate reply received.")
        cert_file = get_cert_file_path(self.topology.isd_id,
            self.topology.ad_id, cert_rep.cert_isd, cert_rep.cert_ad,
            cert_rep.cert_version)
        cert = base64.b64decode(cert_rep.cert).decode('ascii')
        write_file(cert_file, cert)
        if (cert_rep.cert_isd, cert_rep.cert_ad, 0) in self.requested_certs:
            del self.requested_certs[(cert_rep.cert_isd, cert_rep.cert_ad, 0)]
        self.handle_unverified_beacons()

    def process_trc_rep(self, trc_rep):
        """
        Process the TRC reply.
        """
        assert isinstance(trc_rep, TRCReply)
        logging.info("TRC reply received.")
        trc_file = get_trc_file_path(self.topology.isd_id, self.topology.ad_id,
            trc_rep.trc_isd, trc_rep.trc_version)
        trc = base64.b64decode(trc_rep.trc).decode('ascii')
        write_file(trc_file, trc)
        if (trc_rep.trc_isd, trc_rep.trc_version) in self.requested_trcs:
            del self.requested_trcs[(trc_rep.trc_isd, trc_rep.trc_version)]
        self.handle_unverified_beacons()

    def _verify_beacon(self, pcb):
        """
        Once the necessary certificate and TRC files have been found, verify the
        beacons.
        """
        assert isinstance(pcb, PathSegment)
        last_ad = pcb.ads[-1]
        cert_isd = last_ad.pcbm.spcbf.isd_id
        cert_ad = last_ad.pcbm.ad_id
        trc_version = pcb.rotf.rot_version
        subject = 'ISD:' + str(cert_isd) + '-AD:' + str(cert_ad)
        cert_file = get_cert_file_path(self.topology.isd_id,
            self.topology.ad_id, cert_isd, cert_ad, 0)
        if os.path.exists(cert_file):
            chain = CertificateChain(cert_file)
        else:
            chain = CertificateChain.from_values([])
        trc_file = get_trc_file_path(self.topology.isd_id, self.topology.ad_id,
            cert_isd, trc_version)
        trc = TRC(trc_file)
        data_to_verify = (str(cert_ad) + str(last_ad.pcbm.hof) +
            str(last_ad.pcbm.spcbf))
        for peer_marking in last_ad.pms:
            data_to_verify += str(peer_marking)
        return verify(data_to_verify, last_ad.sig, subject, chain, trc,
            trc_version)

    def handle_unverified_beacons(self):
        """
        Handle beacons which are waiting to be verified.
        """
        for index in range(len(self.unverified_beacons)):
            pcb = self.unverified_beacons.popleft()
            cert_isd = pcb.get_last_pcbm().spcbf.isd_id
            cert_ad = pcb.get_last_pcbm().ad_id
            trc_version = pcb.rotf.rot_version
            if self._check_certs_trc(cert_isd, cert_ad, trc_version,
                pcb.rotf.if_id):
                if self._verify_beacon(pcb):
                    self.registered_beacons.append(pcb)
                    self.beacons.append(pcb)
                    logging.info("Registered valid beacon.")
                else:
                    logging.info("Invalid beacon.")
            else:
                logging.debug("Certificate(s) or TRC missing.")
                self.unverified_beacons.append(pcb)


def main():
    """
    Main function.
    """
    logging.basicConfig(level=logging.DEBUG)
    if len(sys.argv) != 5:
        logging.info("run: %s <core|local> IP topo_file conf_file", sys.argv[0])
        sys.exit()

    if sys.argv[1] == "core":
        beacon_server = CoreBeaconServer(IPv4HostAddr(sys.argv[2]), sys.argv[3],
                                         sys.argv[4])
    elif sys.argv[1] == "local":
        beacon_server = LocalBeaconServer(IPv4HostAddr(sys.argv[2]),
                                          sys.argv[3],
                                          sys.argv[4])
    else:
        logging.error("First parameter can only be 'local' or 'core'!")
        sys.exit()
    beacon_server.run()

if __name__ == "__main__":
    main()
