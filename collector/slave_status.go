// Copyright 2018 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Scrape `SHOW SLAVE STATUS`.

package collector

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	// Subsystem.
	slaveStatus = "slave_status"
)

var slaveStatusQueries = [3]string{"SHOW ALL SLAVES STATUS", "SHOW SLAVE STATUS", "SHOW REPLICA STATUS"}
var slaveStatusQuerySuffixes = [3]string{" NONBLOCKING", " NOLOCK", ""}

func columnIndex(slaveCols []string, colName string) int {
	for idx := range slaveCols {
		if slaveCols[idx] == colName {
			return idx
		}
	}
	return -1
}

func columnValue(scanArgs []interface{}, slaveCols []string, colName string) string {
	var columnIndex = columnIndex(slaveCols, colName)
	if columnIndex == -1 {
		return ""
	}
	return string(*scanArgs[columnIndex].(*sql.RawBytes))
}

// ScrapeSlaveStatus collects from `SHOW SLAVE STATUS`.
type ScrapeSlaveStatus struct{}

// Name of the Scraper. Should be unique.
func (ScrapeSlaveStatus) Name() string {
	return slaveStatus
}

// Help describes the role of the Scraper.
func (ScrapeSlaveStatus) Help() string {
	return "Collect from SHOW SLAVE STATUS"
}

// Version of MySQL from which scraper is available.
func (ScrapeSlaveStatus) Version() float64 {
	return 5.1
}

// Scrape collects data from database connection and sends it over channel as prometheus metric.
func (ScrapeSlaveStatus) Scrape(ctx context.Context, instance *instance, ch chan<- prometheus.Metric, logger *slog.Logger) error {
	var (
		slaveStatusRows *sql.Rows
		err             error
	)
	db := instance.getDB()
	// Try the both syntax for MySQL/Percona and MariaDB
	for _, query := range slaveStatusQueries {
		slaveStatusRows, err = db.QueryContext(ctx, query)
		if err != nil { // MySQL/Percona
			// Leverage lock-free SHOW SLAVE STATUS by guessing the right suffix
			for _, suffix := range slaveStatusQuerySuffixes {
				slaveStatusRows, err = db.QueryContext(ctx, fmt.Sprint(query, suffix))
				if err == nil {
					break
				}
			}
		} else { // MariaDB
			break
		}
	}
	if err != nil {
		return err
	}
	defer slaveStatusRows.Close()

	slaveCols, err := slaveStatusRows.Columns()
	if err != nil {
		return err
	}

	for slaveStatusRows.Next() {
		// As the number of columns varies with mysqld versions,
		// and sql.Scan requires []interface{}, we need to create a
		// slice of pointers to the elements of slaveData.
		scanArgs := make([]interface{}, len(slaveCols))
		for i := range scanArgs {
			scanArgs[i] = &sql.RawBytes{}
		}

		if err := slaveStatusRows.Scan(scanArgs...); err != nil {
			return err
		}

		masterUUID := columnValue(scanArgs, slaveCols, "Master_UUID")
		if masterUUID == "" {
			masterUUID = columnValue(scanArgs, slaveCols, "Source_UUID")
		}
		masterHost := columnValue(scanArgs, slaveCols, "Master_Host")
		if masterHost == "" {
			masterHost = columnValue(scanArgs, slaveCols, "Source_Host")
		}
		channelName := columnValue(scanArgs, slaveCols, "Channel_Name")       // MySQL & Percona
		connectionName := columnValue(scanArgs, slaveCols, "Connection_name") // MariaDB

		for i, col := range slaveCols {
			if value, ok := parseStatus(*scanArgs[i].(*sql.RawBytes)); ok { // Silently skip unparsable values.
				ch <- prometheus.MustNewConstMetric(
					prometheus.NewDesc(
						prometheus.BuildFQName(namespace, slaveStatus, strings.ToLower(col)),
						"Generic metric from SHOW SLAVE STATUS.",
						[]string{"master_host", "master_uuid", "channel_name", "connection_name"},
						nil,
					),
					prometheus.UntypedValue,
					value,
					masterHost, masterUUID, channelName, connectionName,
				)
			}
		}

		parseMariaDBGtid(ch, "Gtid_IO_Pos", columnValue(scanArgs, slaveCols, "Gtid_IO_Pos"), masterHost, masterUUID, channelName, connectionName)
		parseMariaDBGtid(ch, "Gtid_Slave_Pos", columnValue(scanArgs, slaveCols, "Gtid_Slave_Pos"), masterHost, masterUUID, channelName, connectionName)
	}
	return nil
}

func parseMariaDBGtid(ch chan<- prometheus.Metric, name string, value string, masterHost string, masterUUID string, channelName string, connectionName string) {
	if value == "" {
		return
	}

	for _, gtid := range strings.Split(value, ",") {
		parts := strings.Split(gtid, "-")
		if len(parts) != 3 {
			continue
		}

		domainID := parts[0]
		serverID := parts[1]

		sequence_num, err := strconv.ParseUint(parts[2], 10, 64)
		if err != nil {
			continue
		}

		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc(
				prometheus.BuildFQName(namespace, slaveStatus, strings.ToLower(name)),
				fmt.Sprintf("%s metric from SHOW SLAVE STATUS.", name),
				[]string{"master_host", "master_uuid", "channel_name", "connection_name", "domain_id", "server_id"},
				nil,
			),
			prometheus.GaugeValue,
			float64(sequence_num),
			masterHost, masterUUID, channelName, connectionName, domainID, serverID,
		)
	}
}

// check interface
var _ Scraper = ScrapeSlaveStatus{}
