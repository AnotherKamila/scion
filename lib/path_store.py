#path_store.py

#Copyright 2014 ETH Zurich

#Licensed under the Apache License, Version 2.0 (the "License");
#you may not use this file except in compliance with the License.
#You may obtain a copy of the License at

#http://www.apache.org/licenses/LICENSE-2.0

#Unless required by applicable law or agreed to in writing, software
#distributed under the License is distributed on an "AS IS" BASIS,
#WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#See the License for the specific language governing permissions and
#limitations under the License.

"""
:mod:`path_store` --- Path record storage and selection for path servers
========================================================================
"""

from collections import defaultdict
from external.sorted_collection import SortedCollection
from lib.packet.pcb import PathSegment
from Crypto.Hash import SHA256
import json
import logging
import random
import sys
import time


class PathPolicy(object):
    """
    Stores a path policy.
    """
    def __init__(self, path_policy_file=None):
        self.best_set_size = 5
        self.candidates_set_size = 600
        self.history_limit = 0
        self.update_after_number = 0
        self.update_after_time = 0
        self.unwanted_ads = []
        self.property_ranges = {}
        self.property_weights = {}
        if path_policy_file:
            self.parse(path_policy_file)

    def get_path_policy_dict(self):
        path_policy_dict = {'best_set_size': self.best_set_size,
                            'candidates_set_size': self.candidates_set_size,
                            'history_limit': self.history_limit,
                            'update_after_number': self.update_after_number,
                            'update_after_time': self.update_after_time,
                            'unwanted_ads': self.unwanted_ads,
                            'property_ranges': self.property_ranges,
                            'property_weights': self.property_weights}
        return path_policy_dict

    def parse(self, path_policy_file):
        """
        Parses the policies in the path store config file.
        """
        try:
            with open(path_policy_file) as path_policy_fh:
                path_policy = json.load(path_policy_fh)
        except (ValueError, KeyError, TypeError):
            logging.error("PathPolicy: JSON format error.")
            return
        self.best_set_size = path_policy['BestSetSize']
        self.candidates_set_size = path_policy['CandidatesSetSize']
        self.history_limit = path_policy['HistoryLimit']
        self.update_after_number = path_policy['UpdateAfterNumber']
        self.update_after_time = path_policy['UpdateAfterTime']
        unwanted_ads = path_policy['UnwantedADs'].split(',')
        for unwanted_ad in unwanted_ads:
            unwanted_ad = unwanted_ad.split('-')
            unwanted_ad = (int(unwanted_ad[0]), int(unwanted_ad[1]))
            self.unwanted_ads.append(unwanted_ad)
        property_ranges = path_policy['PropertyRanges']
        for key in property_ranges:
            property_range = property_ranges[key].split('-')
            property_range = (int(property_range[0]), int(property_range[1]))
            self.property_ranges[key] = property_range
        self.property_weights = path_policy['PropertyWeights']

    def __str__(self):
        path_policy_dict = self.get_path_policy_dict()
        path_policy_str = json.dumps(path_policy_dict, sort_keys=True, indent=4)
        return path_policy_str


class PathStoreRecord(object):
    """
    Path record that gets stored in the the PathStore.

    :ivar pcb: the PCB representing the record.
    :vartype pcb: :class:`lib.packet.pcb.PathSegment`
    :ivar id: the path segment identifier stored in the record's PCB.
    :vartype id: bytes
    :ivar fidelity: the fidelity of the path record.
    :vartype fidelity: float
    :ivar peer_links: the number of peering links in the path segment.
    :vartype peer_links: int
    :ivar hops_length: the length of the path segment in hops.
    :vartype hops_length: int
    :ivar disjointness: the disjointness of the path segment compared to the
       other paths in the PathStore database.
    :vartype disjointness: int
    :ivar last_sent_time: the Unix time at which the path segment was last
       sent.
    :vartype last_sent_time: int
    :ivar last_seen_time: the Unix time at which the path segment was last seen
       by the path server.
    :vartype last_seen_time: int
    :ivar delay_time: the time in seconds between the PCB's creation and the
       time it was last seen by the path server.
    :vartype delay_time: int
    :ivar guaranteed_bandwidth: the path segment's guaranteed bandwidth.
    :vartype guaranteed_bandwidth: int
    :ivar available_bandwidth: the path segment's available bandwidth.
    :vartype available_bandwidth: int
    :ivar total_bandwidth: the path segment's total bandwidth.
    :vartype total_bandwidth: int
    """

    def __init__(self, pcb):
        assert isinstance(pcb, PathSegment)
        self.pcb = pcb
        self.id = pcb.segment_id
        self.fidelity = 0
        self.peer_links = pcb.get_n_peer_links()
        self.hops_length = pcb.get_n_hops()
        self.disjointness = 0
        self.last_sent_time = 0
        self.last_seen_time = int(time.time())
        self.delay_time = self.last_seen_time - pcb.get_timestamp()
        self.guaranteed_bandwidth = 0
        self.available_bandwidth = 0
        self.total_bandwidth = 0

    def update_fidelity(self, path_policy):
        """
        Computes a path fidelity based on all path properties and considering
        the corresponding weights, which are stored in the path policy.
        """
        self.fidelity = 0
        self.fidelity += (path_policy.property_weights['PeerLinks'] *
                          self.peer_links)
        self.fidelity += (1.0 * path_policy.property_weights['HopsLength'] /
                          (self.hops_length))
        self.fidelity += (path_policy.property_weights['Disjointness'] *
                          self.disjointness)
        self.fidelity += (path_policy.property_weights['LastSentTime'] *
                          (int(time.time()) - self.last_sent_time)**2)
        self.fidelity += (path_policy.property_weights['LastSeenTime'] *
                          self.last_seen_time)
        self.fidelity += (1.0 * path_policy.property_weights['DelayTime'] /
                          self.delay_time)
        self.fidelity += (path_policy.property_weights['GuaranteedBandwidth'] *
                          self.guaranteed_bandwidth)
        self.fidelity += (path_policy.property_weights['AvailableBandwidth'] *
                          self.available_bandwidth)
        self.fidelity += (path_policy.property_weights['TotalBandwidth'] *
                          self.total_bandwidth)

    def __eq__(self, other):
        if type(other) is type(self):
            return self.id == other.id
        else:
            return False

    def __str__(self):
        path_info_str = ''.join(["[Path]\n", str(self.pcb), "ID: ",
                                 str(self.id), "\n", "Fidelity: ",
                                 str(self.fidelity), "\n"])
        return path_info_str


class PathStore(object):
    """
    Path Store class.
    """

    def __init__(self, path_policy):
        self.path_policy = path_policy
        self.candidates = []
        self.best_paths_history = []

    def add_record(self, record):
        """
        Possibly add a new path to the candidates list.

        :param pcb: The PCB representing the potential path.
        :type pcb: PathSegment
        """
        assert isinstance(record, PathStoreRecord)
        for index in range(len(self.candidates)):
            if self.candidates[index] == record:
                record.last_sent_time = self.candidates[index].last_sent_time
                del self.candidates[index]
                break
        self.candidates.append(record)
        self._update_all_disjointness()
        self._update_all_fidelity()
        self.candidates = sorted(self.candidates, key=lambda x: x.fidelity)
        if len(self.candidates) > self.path_policy.candidates_set_size:
            self.candidates = self.candidates[1:] # first or last depending on
                                                  # how SortCollection sorts
                                                  # (i.e. ASC or DESC)

    def _update_all_disjointness(self):
        """
        Update the disjointness of all path candidates.
        """
        ad_count = defaultdict(int)
        for candidate in self.candidates:
            for ad_marking in candidate.pcb.ads:
                ad_count[ad_marking.pcbm.ad_id] += 1
        for candidate in self.candidates:
            candidate.disjointness = 0
            for ad_marking in candidate.pcb.ads:
                candidate.disjointness += ad_count[ad_marking.pcbm.ad_id]


    def _update_all_fidelity(self):
        """
        Update the fidelity of all path candidates.
        """
        for candidate in self.candidates:
            candidate.update_fidelity(self.path_policy)

    def get_candidates(self, k=10):
        """
        Returns k path candidates from the temporary buffer.
        """
        return self.candidates[:k]

    def get_last_selection(self, k=10):
        """
        Returns the latest k best paths from the history.
        """
        return self.best_paths_history[0][:k]

    def get_paths(self, k=10):
        """
        Returns the latest k best paths.
        """
        return reversed(self.candidates[-k:])

    def store_selection(self, k=10):
        """
        Stores the best k paths into the path history and reset the list of
        candidates.

        .. warning::
           This function makes use of the `list.clear()` method and thus
           requires Python 3.3 or greater.
        """
        self.best_paths_history.insert(0, self.get_paths(k))
        self.candidates.clear()

    def __str__(self):
        path_store_str = "[PathStore]\n"
        path_store_str += str(self.path_policy) + "\n"
        for candidate in self.candidates:
            path_store_str += str(candidate)
        return path_store_str
