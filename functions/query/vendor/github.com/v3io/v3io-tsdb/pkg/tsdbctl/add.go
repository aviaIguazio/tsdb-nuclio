/*
Copyright 2018 Iguazio Systems Ltd.

Licensed under the Apache License, Version 2.0 (the "License") with
an addition restriction as set forth herein. You may not use this
file except in compliance with the License. You may obtain a copy of
the License at http://www.apache.org/licenses/LICENSE-2.0.

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
implied. See the License for the specific language governing
permissions and limitations under the License.

In addition, you may not use the software for any purposes that are
illegal under applicable law, and the grant of the foregoing license
under the Apache 2.0 license is conditioned upon your compliance with
such restriction.
*/

package tsdbctl

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/v3io/v3io-tsdb/pkg/config"
	"github.com/v3io/v3io-tsdb/pkg/tsdb"
	"github.com/v3io/v3io-tsdb/pkg/utils"
)

const ArraySeparator = ","

type addCommandeer struct {
	cmd            *cobra.Command
	rootCommandeer *RootCommandeer
	name           string
	lset           string
	tArr           string
	vArr           string
	inFile         string
	stdin          bool
	delay          int
}

func newAddCommandeer(rootCommandeer *RootCommandeer) *addCommandeer {
	commandeer := &addCommandeer{
		rootCommandeer: rootCommandeer,
	}

	cmd := &cobra.Command{
		Aliases: []string{"append"},
		Use:     "add [<metric>] [<labels>] [flags]",
		Short:   "Add metric samples to a TSDB instance",
		Long:    `Add (ingest) metric samples into a TSDB instance (table).`,
		Example: `The examples assume that the endpoint of the web-gateway service, the login credentials, and
the name of the data container are configured in the default configuration file (` + config.DefaultConfigurationFileName + `)
instead of using the -s|--server, -u|--username, -p|--password, and -c|--container flags.
- tsdbctl add temperature -t mytsdb -d 28 -m now-2h
- tsdbctl add http_req method=get -t mytsdb -d 99.9
- tsdbctl add cpu "host=A,os=win" -t metrics-table -d "73.2,45.1" -m "1533026403000,now-1d"
- tsdbctl add -t perfstats -f ~/tsdb/tsdb_input.csv
- tsdbctl add log -t mytsdb -m now-2h -d "This thing has just happened"

Notes:
- The command requires a metric name and one or more sample values.
  You can provide this information either by using the <metric> argument and the -d|--values flag,
  or by using the -f|--file flag to point to a CSV file that contains the required information.  
- It is possible to ingest metrics containing string values, Though a single metric can contain either Floats or Strings, But not both.

Arguments:
  <metric> (string) The name of the metric for which to add samples.
                    The metric name must be provided either in this argument or in a
                    CSV file that is specified with the -f|--file flag.
  <labels> (string) An optional list of labels to add, as a comma-separated list of
                    "<label name>=<label value>" key-value pairs.
                    This argument is applicable only when setting the <metric> argument.
                    You can also optionally define labels to add to specific metrics in a
                    CSV file that is specified with the -f|--file command.`,
		RunE: func(cmd *cobra.Command, args []string) error {

			if commandeer.inFile != "" && commandeer.stdin {
				return errors.New("-f|--file and --stdin are mutually exclusive.")
			}

			if commandeer.inFile == "" && !commandeer.stdin {
				// if its not using an input CSV file check for name & labels arguments
				if len(args) == 0 {
					return errors.New("add require metric name and/or labels")
				}

				commandeer.name = args[0]

				if len(args) > 1 {
					commandeer.lset = args[1]
				}
			}

			return commandeer.add()

		},
	}

	cmd.Flags().StringVarP(&commandeer.tArr, "times", "m", "",
		"An array of metric-sample times, as a comma-separated list of times\nspecified as Unix timestamps in milliseconds or as relative times of the\nformat \"now\" or \"now-[0-9]+[mhd]\" (where 'm' = minutes, 'h' = hours,\nand 'd' = days). Note that an ingested sample time cannot be earlier\nthan the latest previously ingested sample time for the same metric.\nThis includes metrics ingested in the same command, so specify the\ningestion times in ascending chronological order. Example:\n\"1537971020000,now-2d,now-95m,now\".\nThe default sample time is the current time (\"now\").")
	cmd.Flags().StringVarP(&commandeer.vArr, "values", "d", "",
		"An array of metric-sample data values, as a comma-separated list of\ninteger, float or string values. Example: \"99.3,82.12,25.87,100\".\nThe command requires at least one metric value, which can be provided\nwith this flag or in a CSV file that is set with the -f|--file flag.")
	cmd.Flags().StringVarP(&commandeer.inFile, "file", "f", "",
		"Path to a CSV metric-samples input file with rows of this format:\n  <metric name>,[<labels>],<sample data value>[,<sample time>]\nNote that all rows must have the same number of columns.\nExamples: \"~/tests/tsdb_samples.csv\" where the file has this content:\n  temp,degree=cel,28,1529659800000\n  cpu,\"os=win,id=82\",78.5,now-1h\n  volume,,6104.02,\n\"my_metrics.csv\" where the file has this content (no time column):\n  noise,,50\n  cpu2,\"os=linux,id=2\",95")
	cmd.Flags().BoolVar(&commandeer.stdin, "stdin", false,
		"Read from standard input")
	cmd.Flags().Lookup("stdin").Hidden = true
	cmd.Flags().IntVar(&commandeer.delay,
		"delay", 0, "A delay period, in milliseconds, to apply between ingested sample batches")
	cmd.Flags().Lookup("delay").Hidden = true

	commandeer.cmd = cmd

	return commandeer
}

func (ac *addCommandeer) add() error {

	var err error
	var lset utils.Labels

	// Initialize parameters and the adapter
	if err = ac.rootCommandeer.initialize(); err != nil {
		return err
	}

	// Start the adapter
	if err = ac.rootCommandeer.startAdapter(); err != nil {
		return err
	}

	appender, err := ac.rootCommandeer.adapter.Appender()
	if err != nil {
		return errors.Wrap(err, "Failed to create an adapter Appender.")
	}

	if ac.inFile == "" && !ac.stdin {
		// Process direct CLI input
		if lset, err = utils.LabelsFromStringWithName(ac.name, ac.lset); err != nil {
			return err
		}

		if ac.vArr == "" {
			return errors.New("The metric-samples array must have at least one value (currently empty).")
		}

		tarray, varray, err := strToTV(ac.tArr, ac.vArr)
		if err != nil {
			return err
		}

		_, err = ac.appendMetric(appender, lset, tarray, varray)
		if err != nil {
			return err
		}

		_, err = appender.WaitForCompletion(0)
		return err
	}

	err = ac.appendMetrics(appender, lset)
	if err != nil {
		return err
	}

	// Ensure that all writes are committed
	_, err = appender.WaitForCompletion(0)
	if err != nil {
		return errors.Wrap(err, "Operation timed out.")
	}

	ac.rootCommandeer.logger.Info("Done!")
	return nil
}

func (ac *addCommandeer) appendMetrics(append tsdb.Appender, lset utils.Labels) error {

	var fp *os.File
	var err error
	if ac.inFile == "" {
		fp = os.Stdin
	} else {
		fp, err = os.Open(ac.inFile)
		if err != nil {
			return errors.Wrapf(err, "Failed to open the CSV input file.")
		}
	}
	defer fp.Close()

	r := csv.NewReader(fp)

	for num := 0; true; num++ {

		line, err := r.Read()
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return errors.Wrap(err, "Failed to read a CSV record.")
			}
		}

		// Print a period (dot) on every 1000 inserts
		if num%1000 == 999 {
			fmt.Printf(".")
			if ac.delay > 0 {
				time.Sleep(time.Duration(ac.delay) * time.Millisecond)
			}
		}

		if len(line) < 3 || len(line) > 4 {
			return fmt.Errorf("Line %d of the CSV input file (%v) doesn't conform to the CSV-record requirements of 3-4 columns in each row - metric name,labels,value,[time]", num, line)
		}

		if lset, err = utils.LabelsFromStringWithName(line[0], line[1]); err != nil {
			return err
		}

		tarr := ""
		if len(line) == 4 {
			tarr = line[3]
		}

		tarray, varray, err := strToTV(tarr, line[2])
		if err != nil {
			return err
		}

		_, err = ac.appendMetric(append, lset, tarray, varray)
		if err != nil {
			return err
		}

	}

	return nil
}

func (ac *addCommandeer) appendMetric(
	append tsdb.Appender, lset utils.Labels, tarray []int64, varray []interface{}) (uint64, error) {

	ac.rootCommandeer.logger.DebugWith("Adding a sample value to a metric.", "lset", lset, "t", tarray, "v", varray)

	ref, err := append.Add(lset, tarray[0], varray[0])
	if err != nil {
		return 0, errors.Wrap(err, "Failed to add a sample value to a metric.")
	}

	for i := 1; i < len(varray); i++ {
		err := append.AddFast(lset, ref, tarray[i], varray[i])
		if err != nil {
			return 0, errors.Wrap(err, "Failed to perform AddFast append of metric sample values.")
		}
	}

	return ref, nil
}

func strToTV(tarr, varr string) ([]int64, []interface{}, error) {

	tlist := strings.Split(tarr, ArraySeparator)
	vlist := strings.Split(varr, ArraySeparator)

	if tarr == "" && len(vlist) > 1 {
		return nil, nil, errors.New("A times array must be provided when providing a values array.")
	}

	if tarr != "" && len(tlist) != len(vlist) {
		return nil, nil, errors.New("The times and values arrays don't have the same amount of elements.")
	}

	var tarray []int64
	var varray []interface{}

	var isFloats bool
	for i := 0; i < len(vlist); i++ {
		v, err := strconv.ParseFloat(vlist[i], 64)
		// If we can parse it as float, use float. Otherwise keep the value as is and it will be encoded using the variant encoder
		if err != nil && !isFloats {
			varray = append(varray, vlist[i])
		} else if err != nil && isFloats {
			return nil, nil, errors.WithStack(err)
		} else {
			isFloats = true
			varray = append(varray, v)
		}
	}

	now := int64(time.Now().Unix() * 1000)
	if tarr == "" {
		tarray = append(tarray, now)
	} else {
		for i := 0; i < len(vlist); i++ {
			tstr := strings.TrimSpace(tlist[i])
			if tstr == "now" || tstr == "now-" {
				tarray = append(tarray, now)
			} else if strings.HasPrefix(tstr, "now-") {
				t, err := utils.Str2duration(tstr[4:])
				if err != nil {
					return nil, nil, errors.Wrap(err, "Failed to parse the pattern following 'now-'.")
				}
				tarray = append(tarray, now-int64(t))
			} else {
				t, err := strconv.Atoi(tlist[i])
				if err != nil {
					return nil, nil, errors.Wrap(err, "Invalid time format. Supported formats are Unix timesamps in milliseconds and relative times of the format 'now' or 'now-[0-9]+[mdh]' (such as 'now-2h').")
				}
				tarray = append(tarray, int64(t))
			}
		}
	}

	return tarray, varray, nil
}
