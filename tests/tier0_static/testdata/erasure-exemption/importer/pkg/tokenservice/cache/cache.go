package cache

type Cache struct{}

func (c *Cache) Put(string) error { return nil }

func (c *Cache) warm() { _ = c.Put("self") }
