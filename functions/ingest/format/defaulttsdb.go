package format

import (
	"encoding/json"
	"github.com/nuclio/nuclio-sdk-go"
	"github.com/pkg/errors"
	"github.com/v3io/v3io-tsdb/pkg/tsdb"
	"github.com/v3io/v3io-tsdb/pkg/utils"
)

/*
Example event:

{
		"metric": "cpu",
		"labels": {
			"dc": "7",
			"hostname": "mybesthost"
		},
		"samples": [
			{
				"t": "1532595945142",
				"v": {
					"N": 95.2
				}
			},
			{
				"t": "1532595948517",
				"v": {
					"n": 86.8
				}
			}
		]
}
*/

type value struct {
	N float64 `json:"n,omitempty"`
}

type sample struct {
	Time  string `json:"t"`
	Value value  `json:"v"`
}

type request struct {
	Metric  string            `json:"metric"`
	Labels  map[string]string `json:"labels,omitempty"`
	Samples []sample          `json:"samples"`
}

//implements InputFormat
type defaultTsdb struct{}

func (Ingester defaultTsdb) Ingest(tsdbAppender tsdb.Appender, event nuclio.Event) error {
	var request request

	// parse body
	if err := json.Unmarshal(event.GetBody(), &request); err != nil {
		return nuclio.WrapErrBadRequest(err)
	}

	// convert the map[string]string -> []Labels
	labels := getLabelsFromRequest(request.Metric, request.Labels)

	var ref uint64
	// iterate over request samples
	for _, sample := range request.Samples {

		// if time is not specified assume "now"
		if sample.Time == "" {
			sample.Time = "now"
		}

		// convert time string to time int, string can be: now, now-2h, int (unix milisec time), or RFC3339 date string
		sampleTime, err := utils.Str2unixTime(sample.Time)
		if err != nil {
			return errors.Wrap(err, "Failed to parse time: "+sample.Time)
		}

		// append sample to metric
		if ref == 0 {
			ref, err = tsdbAppender.Add(labels, sampleTime, sample.Value.N)
		} else {
			err = tsdbAppender.AddFast(labels, ref, sampleTime, sample.Value.N)
		}
		if err != nil {
			return errors.Wrap(err, "Failed to add sample")
		}
	}
	return nil
}
