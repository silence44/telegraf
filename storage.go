package telegraf

type Storage struct {
	items map[string][]interface{}
}

func (storage *Storage) Add(key string, item interface{}) {
	storage.items[key] = append(storage.items[key], item)
}

func (storage *Storage) Get(key string) ([]interface{}, bool) {
	val, ok := storage.items[key]

	return val, ok
}

var GlobalStorage = &Storage{
	items: make(map[string][]interface{}),
}
