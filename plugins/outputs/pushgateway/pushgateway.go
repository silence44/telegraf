package pushgateway

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/internal"
	"github.com/influxdata/telegraf/plugins/outputs"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
)

var invalidNameCharRE = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// SampleID uniquely identifies a Sample
type SampleID string

// Sample represents the current value of a series.
type Sample struct {
	// Labels are the Prometheus labels.
	Labels map[string]string
	// Value is the value in the Prometheus output. Only one of these will populated.
	Value          float64
	HistogramValue map[float64]uint64
	SummaryValue   map[float64]float64
	// Histograms and Summaries need a count and a sum
	Count uint64
	Sum   float64
	// Expiration is the deadline that this Sample is valid until.
	Expiration time.Time
}

// MetricFamily contains the data required to build valid prometheus Metrics.
type MetricFamily struct {
	// Samples are the Sample belonging to this MetricFamily.
	Samples map[SampleID]*Sample
	// Need the telegraf ValueType because there isn't a Prometheus ValueType
	// representing Histogram or Summary
	TelegrafValueType telegraf.ValueType
	// LabelSet is the label counts for all Samples.
	LabelSet map[string]int
}

type Pushgateway struct {
	Server             string            `toml:"server"`
	TLSCert            string            `toml:"tls_cert"`
	TLSKey             string            `toml:"tls_key"`
	BasicUsername      string            `toml:"basic_username"`
	BasicPassword      string            `toml:"basic_password"`
	ExpirationInterval internal.Duration `toml:"expiration_interval"`
	Path               string            `toml:"path"`
	CollectorsExclude  []string          `toml:"collectors_exclude"`
	StringAsLabel      bool              `toml:"string_as_label"`

	server *http.Server

	sync.Mutex
	// fam is the non-expired MetricFamily by Prometheus metric name.
	fam map[string]*MetricFamily
	// now returns the current time.
	now func() time.Time
}

var sampleConfig = `
  # server = localhost:9091

  # Unless set to false all string metrics will be sent as labels.
  # string_as_label = true
`

func (p *Pushgateway) Start() error {

	log.Println("Will push to pushgateway :" + p.Server)

	return nil
}

func (p *Pushgateway) Stop() {
	// plugin gets cleaned up in Close() already.
}

func (p *Pushgateway) Connect() error {
	// This service output does not need to make any further connections
	return nil
}

func (p *Pushgateway) Close() error {

	return nil
}

func (p *Pushgateway) SampleConfig() string {
	return sampleConfig
}

func (p *Pushgateway) Description() string {
	return "Configuration for the Prometheus client to spawn"
}

// Implements prometheus.Collector
func (p *Pushgateway) Describe(ch chan<- *prometheus.Desc) {
	prometheus.NewGauge(prometheus.GaugeOpts{Name: "Dummy", Help: "Dummy"}).Describe(ch)
}

// Expire removes Samples that have expired.
func (p *Pushgateway) Expire() {
	now := p.now()
	for name, family := range p.fam {
		for key, sample := range family.Samples {
			if p.ExpirationInterval.Duration != 0 && now.After(sample.Expiration) {
				for k, _ := range sample.Labels {
					family.LabelSet[k]--
				}
				delete(family.Samples, key)

				if len(family.Samples) == 0 {
					delete(p.fam, name)
				}
			}
		}
	}
}

// Collect implements prometheus.Collector
func (p *Pushgateway) Collect(ch chan<- prometheus.Metric) {
	p.Lock()
	defer p.Unlock()
	p.Expire()
	for name, family := range p.fam {


		// Get list of all labels on MetricFamily
		var labelNames []string
		for k, v := range family.LabelSet {
			if v > 0 {
				labelNames = append(labelNames, k)
			}
		}
		desc := prometheus.NewDesc(name, "Telegraf collected metric", labelNames, nil)

		for _, sample := range family.Samples {
			// Get labels for this sample; unset labels will be set to the
			// empty string
			var labels []string
			for _, label := range labelNames {
				v := sample.Labels[label]
				labels = append(labels, v)
			}

			var metric prometheus.Metric
			var err error
			switch family.TelegrafValueType {
			case telegraf.Summary:
				metric, err = prometheus.NewConstSummary(desc, sample.Count, sample.Sum, sample.SummaryValue, labels...)
			case telegraf.Histogram:
				metric, err = prometheus.NewConstHistogram(desc, sample.Count, sample.Sum, sample.HistogramValue, labels...)
			default:
				metric, err = prometheus.NewConstMetric(desc, getPromValueType(family.TelegrafValueType), sample.Value, labels...)
			}
			if err != nil {
				log.Printf("E! Error creating prometheus metric, "+
					"key: %s, labels: %v,\nerr: %s\n",
					name, labels, err.Error())
			}

			ch <- metric
		}
	}
}

func sanitize(value string) string {
	return invalidNameCharRE.ReplaceAllString(value, "_")
}

func getPromValueType(tt telegraf.ValueType) prometheus.ValueType {
	switch tt {
	case telegraf.Counter:
		return prometheus.CounterValue
	case telegraf.Gauge:
		return prometheus.GaugeValue
	default:
		return prometheus.UntypedValue
	}
}

// CreateSampleID creates a SampleID based on the tags of a telegraf.Metric.
func CreateSampleID(tags map[string]string) SampleID {
	pairs := make([]string, 0, len(tags))
	for k, v := range tags {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Strings(pairs)
	return SampleID(strings.Join(pairs, ","))
}

func addSample(fam *MetricFamily, sample *Sample, sampleID SampleID) {

	for k, _ := range sample.Labels {
		fam.LabelSet[k]++
	}

	fam.Samples[sampleID] = sample
}

func (p *Pushgateway) addMetricFamily(point telegraf.Metric, sample *Sample, mname string, sampleID SampleID) {
	var fam *MetricFamily
	var ok bool
	if fam, ok = p.fam[mname]; !ok {
		fam = &MetricFamily{
			Samples:           make(map[SampleID]*Sample),
			TelegrafValueType: point.Type(),
			LabelSet:          make(map[string]int),
		}
		p.fam[mname] = fam
	}

	addSample(fam, sample, sampleID)
}

func (p *Pushgateway) Write(metrics []telegraf.Metric) error {
	p.Lock()

	now := p.now()

	for _, point := range metrics {
		tags := point.Tags()
		sampleID := CreateSampleID(tags)

		labels := make(map[string]string)
		for k, v := range tags {
			labels[sanitize(k)] = v
		}

		// Prometheus doesn't have a string value type, so convert string
		// fields to labels if enabled.
		if p.StringAsLabel {
			for fn, fv := range point.Fields() {
				switch fv := fv.(type) {
				case string:
					labels[sanitize(fn)] = fv
				}
			}
		}

		switch point.Type() {
		case telegraf.Summary:
			var mname string
			var sum float64
			var count uint64
			summaryvalue := make(map[float64]float64)
			for fn, fv := range point.Fields() {
				var value float64
				switch fv := fv.(type) {
				case int64:
					value = float64(fv)
				case uint64:
					value = float64(fv)
				case float64:
					value = fv
				default:
					continue
				}

				switch fn {
				case "sum":
					sum = value
				case "count":
					count = uint64(value)
				default:
					limit, err := strconv.ParseFloat(fn, 64)
					if err == nil {
						summaryvalue[limit] = value
					}
				}
			}
			sample := &Sample{
				Labels:       labels,
				SummaryValue: summaryvalue,
				Count:        count,
				Sum:          sum,
				Expiration:   now.Add(p.ExpirationInterval.Duration),
			}
			mname = sanitize(point.Name())

			p.addMetricFamily(point, sample, mname, sampleID)

		case telegraf.Histogram:
			var mname string
			var sum float64
			var count uint64
			histogramvalue := make(map[float64]uint64)
			for fn, fv := range point.Fields() {
				var value float64
				switch fv := fv.(type) {
				case int64:
					value = float64(fv)
				case uint64:
					value = float64(fv)
				case float64:
					value = fv
				default:
					continue
				}

				switch fn {
				case "sum":
					sum = value
				case "count":
					count = uint64(value)
				default:
					limit, err := strconv.ParseFloat(fn, 64)
					if err == nil {
						histogramvalue[limit] = uint64(value)
					}
				}
			}
			sample := &Sample{
				Labels:         labels,
				HistogramValue: histogramvalue,
				Count:          count,
				Sum:            sum,
				Expiration:     now.Add(p.ExpirationInterval.Duration),
			}
			mname = sanitize(point.Name())


			p.addMetricFamily(point, sample, mname, sampleID)

		default:
			for fn, fv := range point.Fields() {
				// Ignore string and bool fields.
				var value float64
				switch fv := fv.(type) {
				case int64:
					value = float64(fv)
				case uint64:
					value = float64(fv)
				case float64:
					value = fv
				default:
					continue
				}

				sample := &Sample{
					Labels:     labels,
					Value:      value,
					Expiration: now.Add(p.ExpirationInterval.Duration),
				}

				// Special handling of value field; supports passthrough from
				// the prometheus input.
				var mname string
				switch point.Type() {
				case telegraf.Counter:
					if fn == "counter" {
						mname = sanitize(point.Name())
					}
				case telegraf.Gauge:
					if fn == "gauge" {
						mname = sanitize(point.Name())
					}
				}
				if mname == "" {
					if fn == "value" {
						mname = sanitize(point.Name())
					} else {
						mname = sanitize(fmt.Sprintf("%s_%s", point.Name(), fn))
					}
				}

				p.addMetricFamily(point, sample, mname, sampleID)

			}
		}
	}

	p.Unlock()

	log.Println("Proccessing pushing to pushgateway")
	push.Collectors("telegraf_metric", map[string]string{}, p.Server, p)
	log.Println("Pushed data")

	return nil
}

func init() {
	outputs.Add("pushgateway", func() telegraf.Output {
		return &Pushgateway{
			ExpirationInterval: internal.Duration{Duration: time.Second * 60},
			StringAsLabel:      true,
			fam:                make(map[string]*MetricFamily),
			now:                time.Now,
		}
	})
}
