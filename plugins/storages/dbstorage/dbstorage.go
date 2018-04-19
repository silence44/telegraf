package dbstorage

import "github.com/influxdata/telegraf/plugins/storages"

type DbStorage struct {
	Name string `toml:"name"`
	Host string `toml:"host"`
	Database string `toml:"database"`
	Port string `toml:"port"`
	User string `toml:"user"`
	Password string `toml:"password"`
}

func (db *DbStorage) CreateDSN() string {
	return db.User + ":" + db.Password + "@tcp(" + db.Host + ":" + db.Port + ")/" + db.Database + "?tls=false"
}

func init() {
	storages.Add("dbstorage", func() interface{} {
		return &DbStorage{}
	})
}
