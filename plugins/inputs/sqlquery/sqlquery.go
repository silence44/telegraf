package sqlquery

import (
	"database/sql"
	//"sync"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs"


	"github.com/pkg/errors"
	"github.com/influxdata/telegraf/plugins/storages/dbstorage"
	"log"
	"strconv"
)

type SqlQuery struct {
	SourceName string `toml:"source_name"`
	Query string `toml:"query"`
	Name string `toml:"name"`
	Source *dbstorage.DbStorage
}

func (q *SqlQuery) setupSource() error {
	sources, ok := telegraf.GlobalStorage.Get("dbstorage")

	if !ok {
		return errors.New("Can't find any sources in storage")
	}

	found := false

	for _, source := range sources {
		source := source.(*dbstorage.DbStorage)

		if source.Name == q.SourceName {
			q.Source = source
			found = true
			break
		}
	}

	if !found {
		return errors.New("Can't find source in storage with name: " + q.SourceName)
	}

	return nil
}

var sampleConfig = `
   # interval = "5s"
   # source_name = "my_source_1"
   # query = "SELECT COUNT(*) as status FROM test;"
   # name = "my_statistic_1"
`
func (s *SqlQuery) SampleConfig() string {
	return sampleConfig
}

func (s *SqlQuery) Description() string {
	return "Read metrics from one or many mysql servers by query"
}

func (s *SqlQuery) Gather(acc telegraf.Accumulator) error {
	err := s.setupSource()
	if err != nil {
		return err
	}

	go acc.AddError(s.gatherQuery(acc))

	return nil
}

func (s *SqlQuery) gatherQuery(acc telegraf.Accumulator) error {
	dsn := s.Source.CreateDSN()

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}

	defer db.Close()
	log.Println("Executing query: " + s.Query)

	row := db.QueryRow(s.Query)

	var value float64
	err = row.Scan(&value)

	if err != nil && err != sql.ErrNoRows {
		return err
	}

	log.Println("Query result: " + strconv.FormatFloat(value, 'f', 2, 64))

	fields := make(map[string]interface{})
	fields[s.Name] = value

	tags := map[string]string{"server": s.Source.Name}

	acc.AddFields("query_statistics", fields, tags)

	return nil
}

func init() {
	inputs.Add("sqlquery", func() telegraf.Input {
		return &SqlQuery{}
	})
}
