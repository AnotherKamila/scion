// Copyright 2019 Anapaya Systems
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

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/scionproto/scion/go/lib/prom"
)

// RevocationLabels define the labels attached to revocation metrics.
type RevocationLabels struct {
	Result, Method string
}

// Labels returns the list of labels.
func (l RevocationLabels) Labels() []string {
	return []string{prom.LabelResult, "method"}
}

// Values returns the label values in the order defined by Labels.
func (l RevocationLabels) Values() []string {
	return []string{l.Result, l.Method}
}

type exporterR struct {
	received *prometheus.CounterVec
}

func newRevocation() exporterR {
	ns, sub := Namespace, "revocation"
	l := RevocationLabels{Result: RevNew, Method: RevFromCtrl}
	return exporterR{
		received: prom.NewCounterVecWithLabels(ns, sub, "received_revocations_total",
			"Total number of received revocation msgs.", l),
	}
}

// Received returns receive counter.
func (e *exporterR) Received(l RevocationLabels) prometheus.Counter {
	return e.received.WithLabelValues(l.Values()...)
}
