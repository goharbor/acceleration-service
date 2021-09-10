// Copyright Project Harbor Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package metrics

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var subsystem = "acceleration_service_core"

var Conversion ConversionMetric

type OpWrapper struct {
	OpDuration   *prometheus.HistogramVec
	OpTotal      *prometheus.CounterVec
	OpErrorTotal *prometheus.CounterVec
}

type ConversionMetric struct {
	*OpWrapper
}

func init() {
	Conversion = ConversionMetric{
		OpWrapper: NewOpWrapper("conversions", []string{"op"}),
	}
	prometheus.MustRegister(
		Conversion.OpDuration,
		Conversion.OpTotal,
		Conversion.OpErrorTotal,
	)
}

func NewOpWrapper(scope string, labelNames []string) *OpWrapper {
	return &OpWrapper{
		OpDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Subsystem: subsystem,
				Name:      fmt.Sprintf("%s_op_duration_seconds", scope),
				Help:      fmt.Sprintf("The latency of %s operations in seconds.", scope),
			},
			labelNames,
		),
		OpTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Subsystem: subsystem,
				Name:      fmt.Sprintf("%s_op_total", scope),
				Help:      fmt.Sprintf("How many %s operations.", scope),
			},
			labelNames,
		),
		OpErrorTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Subsystem: subsystem,
				Name:      fmt.Sprintf("%s_op_error_total", scope),
				Help:      fmt.Sprintf("How many %s operation errors.", scope),
			},
			labelNames,
		),
	}
}

func (metrics *OpWrapper) OpWrap(op func() error, lvs ...string) error {
	start := time.Now()

	err := op()
	Duration(err, metrics.OpDuration, start, lvs...)
	CountInc(err, metrics.OpTotal, metrics.OpErrorTotal, lvs...)

	return err
}

func Duration(err error, metric *prometheus.HistogramVec, start time.Time, lvs ...string) {
	if err != nil {
		return
	}
	elapsed := float64(time.Since(start)) / float64(time.Second)
	metric.WithLabelValues(lvs...).Observe(elapsed)
}

func CountInc(err error, totalMetric *prometheus.CounterVec, errorMetric *prometheus.CounterVec, lvs ...string) {
	totalMetric.WithLabelValues(lvs...).Inc()
	if err != nil && errorMetric != nil {
		errorMetric.WithLabelValues(lvs...).Inc()
	}
}

func CountDesc(metric *prometheus.CounterVec, lvs ...string) {
	metric.WithLabelValues(lvs...).Desc()
}

func CountSet(metric *prometheus.GaugeVec, val float64, lvs ...string) {
	metric.WithLabelValues(lvs...).Set(val)
}
