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

// Scrape `information_schema.tables`.

package collector

import (
	"context"
	"log/slog"
	"strings"

	"github.com/alecthomas/kingpin/v2"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	tableSchemaQuery = `
		SELECT
		    TABLE_SCHEMA,
		    TABLE_NAME,
		    TABLE_TYPE,
		    ifnull(ENGINE, 'NONE') as ENGINE,
		    ifnull(VERSION, '0') as VERSION,
		    ifnull(ROW_FORMAT, 'NONE') as ROW_FORMAT,
		    ifnull(TABLE_ROWS, '0') as TABLE_ROWS,
		    ifnull(DATA_LENGTH, '0') as DATA_LENGTH,
		    ifnull(INDEX_LENGTH, '0') as INDEX_LENGTH,
		    ifnull(DATA_FREE, '0') as DATA_FREE,
		    ifnull(CREATE_OPTIONS, 'NONE') as CREATE_OPTIONS
		  FROM information_schema.tables
		  WHERE TABLE_SCHEMA = ?
		`
	dbListQuery = `
		SELECT
		    SCHEMA_NAME
		  FROM information_schema.schemata
		  WHERE SCHEMA_NAME NOT IN ('mysql', 'performance_schema', 'information_schema', 'sys')
		`
)

// Tunable flags.
var (
	tableSchemaDatabases = kingpin.Flag(
		"collect.info_schema.tables.databases",
		"The list of databases to collect table stats for, or '*' for all",
	).Default("*").String()
)

// Metric descriptors.
var (
	infoSchemaTablesVersionDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, informationSchema, "table_version"),
		"The version number of the table's .frm file",
		[]string{"schema", "table", "type", "engine", "row_format", "create_options"}, nil,
	)
	infoSchemaTablesRowsDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, informationSchema, "table_rows"),
		"The estimated number of rows in the table from information_schema.tables",
		[]string{"schema", "table"}, nil,
	)
	infoSchemaTablesSizeDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, informationSchema, "table_size"),
		"The size of the table components from information_schema.tables",
		[]string{"schema", "table", "component"}, nil,
	)
)

// ScrapeTableSchema collects from `information_schema.tables`.
type ScrapeTableSchema struct{}

// Name of the Scraper. Should be unique.
func (ScrapeTableSchema) Name() string {
	return informationSchema + ".tables"
}

// Help describes the role of the Scraper.
func (ScrapeTableSchema) Help() string {
	return "Collect metrics from information_schema.tables"
}

// Version of MySQL from which scraper is available.
func (ScrapeTableSchema) Version() float64 {
	return 5.1
}

// Scrape collects data from database connection and sends it over channel as prometheus metric.
func (ScrapeTableSchema) Scrape(ctx context.Context, instance *instance, ch chan<- prometheus.Metric, logger *slog.Logger) error {
	var dbList []string
	db := instance.getDB()
	if *tableSchemaDatabases == "*" {
		dbListRows, err := db.QueryContext(ctx, dbListQuery)
		if err != nil {
			return err
		}
		defer dbListRows.Close()

		var database string

		for dbListRows.Next() {
			if err := dbListRows.Scan(
				&database,
			); err != nil {
				return err
			}
			dbList = append(dbList, database)
		}
	} else {
		dbList = strings.Split(*tableSchemaDatabases, ",")
	}

	for _, database := range dbList {
		tableSchemaRows, err := db.QueryContext(ctx, tableSchemaQuery, database)
		if err != nil {
			return err
		}
		defer tableSchemaRows.Close()

		var (
			tableSchema   string
			tableName     string
			tableType     string
			engine        string
			version       uint64
			rowFormat     string
			tableRows     uint64
			dataLength    uint64
			indexLength   uint64
			dataFree      uint64
			createOptions string
		)

		for tableSchemaRows.Next() {
			err = tableSchemaRows.Scan(
				&tableSchema,
				&tableName,
				&tableType,
				&engine,
				&version,
				&rowFormat,
				&tableRows,
				&dataLength,
				&indexLength,
				&dataFree,
				&createOptions,
			)
			if err != nil {
				return err
			}
			ch <- prometheus.MustNewConstMetric(
				infoSchemaTablesVersionDesc, prometheus.GaugeValue, float64(version),
				tableSchema, tableName, tableType, engine, rowFormat, createOptions,
			)
			ch <- prometheus.MustNewConstMetric(
				infoSchemaTablesRowsDesc, prometheus.GaugeValue, float64(tableRows),
				tableSchema, tableName,
			)
			ch <- prometheus.MustNewConstMetric(
				infoSchemaTablesSizeDesc, prometheus.GaugeValue, float64(dataLength),
				tableSchema, tableName, "data_length",
			)
			ch <- prometheus.MustNewConstMetric(
				infoSchemaTablesSizeDesc, prometheus.GaugeValue, float64(indexLength),
				tableSchema, tableName, "index_length",
			)
			ch <- prometheus.MustNewConstMetric(
				infoSchemaTablesSizeDesc, prometheus.GaugeValue, float64(dataFree),
				tableSchema, tableName, "data_free",
			)
		}
	}

	return nil
}

// check interface
var _ Scraper = ScrapeTableSchema{}
