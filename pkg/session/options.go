package session

import "sync"

// Options is a thread-safe key-value store with typed defaults.
type Options struct {
	mu       sync.RWMutex
	data     map[string]any
	defaults map[string]any
}

// NewOptions returns an initialized Options store.
func NewOptions() *Options {
	return &Options{
		data:     make(map[string]any),
		defaults: make(map[string]any),
	}
}

// SetDefault registers a default value for the given key.
// The default is returned by Get when no explicit value has been set.
func (o *Options) SetDefault(key string, val any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.defaults[key] = val
}

// Set stores an explicit value for the given key, overriding any default.
func (o *Options) Set(key string, val any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.data[key] = val
}

// Get returns the value for key. If no explicit value has been set, the
// registered default is returned. If neither exists, nil is returned.
func (o *Options) Get(key string) any {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if v, ok := o.data[key]; ok {
		return v
	}
	if v, ok := o.defaults[key]; ok {
		return v
	}
	return nil
}

// GetString returns the value for key as a string.
// Returns the empty string when the value is not a string or is absent.
func (o *Options) GetString(key string) string {
	v := o.Get(key)
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// GetInt returns the value for key as an int.
// Returns 0 when the value is not an int or is absent.
func (o *Options) GetInt(key string) int {
	v := o.Get(key)
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

// GetBool returns the value for key as a bool.
// Returns false when the value is not a bool or is absent.
func (o *Options) GetBool(key string) bool {
	v := o.Get(key)
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// GetFloat returns the value for key as a float64.
// Returns 0 when the value is not numeric or is absent.
func (o *Options) GetFloat(key string) float64 {
	v := o.Get(key)
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}
