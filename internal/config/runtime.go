package config

import (
	"fmt"
	"net"
	"reflect"
	"strings"
	"time"
)

// ValidateListen refuses accidental exposure; AuthDisabled is the explicit opt-out.
func (s Server) ValidateListen() error {
	host := strings.Trim(strings.TrimSpace(s.Host), "[]")
	if host == "" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	if s.AuthDisabled || strings.TrimSpace(s.AuthToken) != "" || s.DashboardLocked() {
		return nil
	}
	return fmt.Errorf("listening on %q requires server.auth_token or a dashboard password; use a loopback address or explicitly set server.auth_disabled", host)
}

// Clone returns an independent configuration, including environment provenance.
func (c *Config) Clone() *Config {
	out := *c
	cloneConfigValue(reflect.ValueOf(&out).Elem())
	return &out
}

func cloneConfigValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Field(i).CanSet() {
				cloneConfigValue(v.Field(i))
			}
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		copy := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			value := reflect.New(v.Type().Elem()).Elem()
			value.Set(iter.Value())
			cloneConfigValue(value)
			copy.SetMapIndex(iter.Key(), value)
		}
		v.Set(copy)
	case reflect.Pointer:
		// display.modules is a *[]string: copying the pointer would leave both
		// clones sharing one backing slice.
		if v.IsNil() {
			return
		}
		copy := reflect.New(v.Type().Elem())
		copy.Elem().Set(v.Elem())
		cloneConfigValue(copy.Elem())
		v.Set(copy)
	case reflect.Slice:
		if v.IsNil() {
			return
		}
		copy := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		reflect.Copy(copy, v)
		for i := range copy.Len() {
			cloneConfigValue(copy.Index(i))
		}
		v.Set(copy)
	}
}

// ReloadMode describes when a saved field becomes effective. Services owning
// processes, connections or caches retain their boot configuration until restart.
func ReloadMode(path string) string {
	for _, prefix := range []string{"database", "logging", "social", "tools.browser", "tools.http", "server.host", "server.port"} {
		if path == prefix || strings.HasPrefix(path, prefix+".") {
			return "restart_required"
		}
	}
	for _, prefix := range []string{"mcp", "terminal", "cron", "rag", "plugins", "roles", "skills.dirs", "gateway"} {
		if path == prefix || strings.HasPrefix(path, prefix+".") {
			return "reconciled"
		}
	}
	return "live"
}

// Effective keeps restart-bound values from the running configuration. The
// desired configuration remains on disk and is never overwritten with this view.
func Effective(current, desired *Config) (*Config, []string) {
	next := desired.Clone()
	var pending []string
	var visit func(reflect.Value, reflect.Value, string)

	visit = func(old, dst reflect.Value, prefix string) {
		typ := old.Type()
		for i := range typ.NumField() {
			name := strings.Split(typ.Field(i).Tag.Get("yaml"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			a, b := old.Field(i), dst.Field(i)
			if a.Kind() == reflect.Struct {
				visit(a, b, path)
				continue
			}
			if ReloadMode(path) == "restart_required" && !reflect.DeepEqual(a.Interface(), b.Interface()) {
				pending = append(pending, path)
				b.Set(a)
			}
		}
	}
	visit(reflect.ValueOf(current.Clone()).Elem(), reflect.ValueOf(next).Elem(), "")
	return next, pending
}

// Validate checks settings whose invalid values would leave a partial reload.
func (c *Config) Validate() error {
	if err := c.Server.ValidateListen(); err != nil {
		return err
	}
	if c.MaxConcurrentSessions < 0 {
		return fmt.Errorf("max_concurrent_sessions must be nonnegative")
	}
	if c.Cron.MaxConcurrent <= 0 || c.Cron.HistoryLimit <= 0 {
		return fmt.Errorf("cron limits must be positive")
	}
	if tz := strings.TrimSpace(c.Cron.Timezone); tz != "" && !strings.EqualFold(tz, "local") {
		if _, err := time.LoadLocation(tz); err != nil {
			return fmt.Errorf("cron.timezone: %w", err)
		}
	}
	if c.Display.Modules != nil {
		if err := ValidateModules(*c.Display.Modules); err != nil {
			return err
		}
	}
	return nil
}
