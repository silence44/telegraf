package storages



type Creator func() interface{}

var Storages = map[string]Creator{}

func Add(name string, creator Creator) {
	Storages[name] = creator
}
