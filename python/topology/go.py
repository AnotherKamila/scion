# Copyright 2014 ETH Zurich
# Copyright 2018 ETH Zurich, Anapaya Systems
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
"""
:mod:`go` --- SCION topology go generator
=============================================
"""
# Stdlib
import os
import toml

# SCION
from lib.app.sciond import get_default_sciond_path
from lib.defines import SCIOND_API_SOCKDIR
from lib.util import write_file
from topology.common import (
    ArgsTopoDicts,
    BR_CONFIG_NAME,
    BS_CONFIG_NAME,
    COMMON_DIR,
    CS_CONFIG_NAME,
    DISP_CONFIG_NAME,
    docker_host,
    get_pub,
    get_pub_ip,
    prom_addr_br,
    prom_addr_infra,
    prom_addr_sciond,
    prom_addr_dispatcher,
    PS_CONFIG_NAME,
    sciond_name,
    SD_CONFIG_NAME,
    trust_db_conf_entry,
)
from topology.prometheus import (
    BS_PROM_PORT,
    CS_PROM_PORT,
    DEFAULT_BR_PROM_PORT,
    PS_PROM_PORT,
    SCIOND_PROM_PORT,
    DISP_PROM_PORT,
)

BS_QUIC_PORT = 30352
PS_QUIC_PORT = 30353
CS_QUIC_PORT = 30354
SD_QUIC_PORT = 0


class GoGenArgs(ArgsTopoDicts):
    def __init__(self, args, topo_dicts, networks, port_gen=None):
        super().__init__(args, topo_dicts, port_gen)
        self.networks = networks


class GoGenerator(object):
    def __init__(self, args):
        """
        :param GoGenArgs args: Contains the passed command line arguments and topo dicts.
        """
        self.args = args
        self.log_dir = '/share/logs' if args.docker else 'logs'
        self.db_dir = '/share/cache' if args.docker else 'gen-cache'
        self.certs_dir = '/share/crypto' if args.docker else 'gen-certs'
        self.log_level = 'trace' if args.trace else 'debug'

    def generate_br(self):
        for topo_id, topo in self.args.topo_dicts.items():
            for k, v in topo.get("BorderRouters", {}).items():
                base = topo_id.base_dir(self.args.output_dir)
                br_conf = self._build_br_conf(topo_id, topo["ISD_AS"], base, k, v)
                write_file(os.path.join(base, k, BR_CONFIG_NAME), toml.dumps(br_conf))

    def _build_br_conf(self, topo_id, ia, base, name, v):
        config_dir = '/share/conf' if self.args.docker else os.path.join(base, name)
        raw_entry = {
            'general': {
                'ID': name,
                'ConfigDir': config_dir,
            },
            'logging': self._log_entry(name),
            'metrics': {
                'Prometheus': prom_addr_br(name, v, DEFAULT_BR_PROM_PORT),
            },
            'discovery': self._discovery_entry(),
            'br': {
                'Profile': False,
            },
        }
        return raw_entry

    def generate_bs(self):
        for topo_id, topo in self.args.topo_dicts.items():
            for elem_id, elem in topo.get("BeaconService", {}).items():
                # only a single Go-BS per AS is currently supported
                if elem_id.endswith("-1"):
                    base = topo_id.base_dir(self.args.output_dir)
                    bs_conf = self._build_bs_conf(topo_id, topo["ISD_AS"], base, elem_id, elem)
                    write_file(os.path.join(base, elem_id, BS_CONFIG_NAME), toml.dumps(bs_conf))

    def _build_bs_conf(self, topo_id, ia, base, name, infra_elem):
        config_dir = '/share/conf' if self.args.docker else os.path.join(base, name)
        raw_entry = {
            'general': {
                'ID': name,
                'ConfigDir': config_dir,
                'ReconnectToDispatcher': True,
            },
            'logging': self._log_entry(name),
            'trustDB': trust_db_conf_entry(self.args, name),
            'beaconDB': beacon_db_conf_entry(self.args, name),
            'discovery': self._discovery_entry(),
            'tracing': self._tracing_entry(),
            'metrics': self._metrics_entry(name, infra_elem, BS_PROM_PORT),
            'quic': self._quic_conf_entry(BS_QUIC_PORT, self.args.svcfrac, infra_elem),
        }
        return raw_entry

    def generate_ps(self):
        for topo_id, topo in self.args.topo_dicts.items():
            for elem_id, elem in topo.get("PathService", {}).items():
                # only a single Go-PS per AS is currently supported
                if elem_id.endswith("-1"):
                    base = topo_id.base_dir(self.args.output_dir)
                    ps_conf = self._build_ps_conf(topo_id, topo["ISD_AS"], base, elem_id, elem)
                    write_file(os.path.join(base, elem_id, PS_CONFIG_NAME), toml.dumps(ps_conf))

    def _build_ps_conf(self, topo_id, ia, base, name, infra_elem):
        config_dir = '/share/conf' if self.args.docker else os.path.join(base, name)
        raw_entry = {
            'general': {
                'ID': name,
                'ConfigDir': config_dir,
                'ReconnectToDispatcher': True,
            },
            'logging': self._log_entry(name),
            'trustDB': trust_db_conf_entry(self.args, name),
            'discovery': self._discovery_entry(),
            'ps': {
                'pathDB': {
                    'Backend': 'sqlite',
                    'Connection': os.path.join(self.db_dir, '%s.path.db' % name),
                },
                'SegSync': True,
            },
            'tracing': self._tracing_entry(),
            'metrics': self._metrics_entry(name, infra_elem, PS_PROM_PORT),
            'quic': self._quic_conf_entry(PS_QUIC_PORT, self.args.svcfrac, infra_elem),
        }
        return raw_entry

    def generate_sciond(self):
        for topo_id, topo in self.args.topo_dicts.items():
            base = topo_id.base_dir(self.args.output_dir)
            sciond_conf = self._build_sciond_conf(topo_id, topo["ISD_AS"], base)
            write_file(os.path.join(base, COMMON_DIR, SD_CONFIG_NAME), toml.dumps(sciond_conf))

    def _build_sciond_conf(self, topo_id, ia, base):
        name = sciond_name(topo_id)
        config_dir = '/share/conf' if self.args.docker else os.path.join(base, COMMON_DIR)
        raw_entry = {
            'general': {
                'ID': name,
                'ConfigDir': config_dir,
                'ReconnectToDispatcher': True,
            },
            'logging': self._log_entry(name),
            'trustDB': trust_db_conf_entry(self.args, name),
            'discovery': self._discovery_entry(),
            'sd': {
                'Reliable': os.path.join(SCIOND_API_SOCKDIR, "%s.sock" % name),
                'Unix': os.path.join(SCIOND_API_SOCKDIR, "%s.unix" % name),
                'Public': '%s,[127.0.0.1]:0' % ia,
                'pathDB': {
                    'Connection': os.path.join(self.db_dir, '%s.path.db' % name),
                },
            },
            'tracing': self._tracing_entry(),
            'metrics': {
                'Prometheus': prom_addr_sciond(self.args.docker, topo_id,
                                               self.args.networks, SCIOND_PROM_PORT)
            },
            'quic': self._quic_conf_entry(SD_QUIC_PORT, self.args.svcfrac),
        }
        return raw_entry

    def generate_cs(self):
        for topo_id, topo in self.args.topo_dicts.items():
            for elem_id, elem in topo.get("CertificateService", {}).items():
                # only a single Go-CS per AS is currently supported
                if elem_id.endswith("-1"):
                    base = topo_id.base_dir(self.args.output_dir)
                    cs_conf = self._build_cs_conf(topo_id, topo["ISD_AS"], base, elem_id, elem)
                    write_file(os.path.join(base, elem_id, CS_CONFIG_NAME), toml.dumps(cs_conf))

    def _build_cs_conf(self, topo_id, ia, base, name, infra_elem):
        config_dir = '/share/conf' if self.args.docker else os.path.join(base, name)
        raw_entry = {
            'general': {
                'ID': name,
                'ConfigDir': config_dir,
                'ReconnectToDispatcher': True,
            },
            'sd_client': {
                'Path': get_default_sciond_path(topo_id),
            },
            'logging': self._log_entry(name),
            'trustDB': trust_db_conf_entry(self.args, name),
            'discovery': self._discovery_entry(),
            'cs': {
                'LeafReissueLeadTime': "6h",
                'IssuerReissueLeadTime': "3d",
                'ReissueRate': "10s",
                'ReissueTimeout': "5s",
            },
            'tracing': self._tracing_entry(),
            'metrics': self._metrics_entry(name, infra_elem, CS_PROM_PORT),
            'quic': self._quic_conf_entry(CS_QUIC_PORT, self.args.svcfrac, infra_elem),
        }
        return raw_entry

    def generate_disp(self):
        if self.args.docker:
            self._gen_disp_docker()
        else:
            elem_dir = os.path.join(self.args.output_dir, "dispatcher")
            config_file_path = os.path.join(elem_dir, DISP_CONFIG_NAME)
            write_file(config_file_path, toml.dumps(self._build_disp_conf("dispatcher")))

    def _gen_disp_docker(self):
        for topo_id, topo in self.args.topo_dicts.items():
            for elem in ["disp", "disp_sig"]:
                elem = "%s_%s" % (elem, topo_id.file_fmt())
                elem_dir = os.path.join(topo_id.base_dir(self.args.output_dir), elem)
                disp_conf = self._build_disp_conf(elem, topo_id)
                write_file(os.path.join(elem_dir, DISP_CONFIG_NAME), toml.dumps(disp_conf))
            for k, _ in topo.get("BorderRouters", {}).items():
                disp_id = '%s_%s%s' % ('disp_br', topo_id.file_fmt(), k[-2:])
                elem_dir = os.path.join(topo_id.base_dir(self.args.output_dir), disp_id)
                disp_conf = self._build_disp_conf(disp_id, topo_id)
                write_file(os.path.join(elem_dir, DISP_CONFIG_NAME), toml.dumps(disp_conf))

    def _build_disp_conf(self, name, topo_id=None):
        prometheus_addr = prom_addr_dispatcher(self.args.docker, topo_id,
                                               self.args.networks, DISP_PROM_PORT, name)
        return {
            'dispatcher': {
                'ID': name,
            },
            'logging': self._log_entry(name),
            'metrics': {
                'Prometheus': prometheus_addr,
            },
        }

    def _discovery_entry(self):
        entry = {
            'static': {
                'Enable': self.args.discovery,
            },
            'dynamic': {
                'Enable': self.args.discovery,
            }
        }
        return entry

    def _tracing_entry(self):
        docker_ip = docker_host(self.args.in_docker, self.args.docker)
        entry = {
            'enabled': True,
            'debug': True,
            'agent': '%s:6831' % docker_ip
        }
        return entry

    def _log_entry(self, name):
        entry = {
            'file': {
                'Path': os.path.join(self.log_dir, "%s.log" % name),
                'Level': self.log_level,
            },
            'console': {
                'Level': 'crit',
            },
        }
        return entry

    def _metrics_entry(self, name, infra_elem, base_port):
        prom_addr = prom_addr_infra(self.args.docker, name, infra_elem, base_port)
        return {
            'Prometheus': prom_addr
        }

    def _quic_conf_entry(self, port, svcfrac, elem=None):
        addr = "127.0.0.1" if elem is None else get_pub_ip(elem["Addrs"])
        if self.args.docker and elem is not None:
            pub = get_pub(elem['Addrs'])
            port = pub['Public']['L4Port']+1
        return {
            'Address':  '[%s]:%s' % (addr, port),
            'CertFile': os.path.join(self.certs_dir, 'tls.pem'),
            'KeyFile': os.path.join(self.certs_dir, 'tls.key'),
            'ResolutionFraction': svcfrac,
        }


def beacon_db_conf_entry(args, name):
    db_dir = '/share/cache' if args.docker else 'gen-cache'
    return {
        'Backend': 'sqlite',
        'Connection': os.path.join(db_dir, '%s.beacon.db' % name),
    }
