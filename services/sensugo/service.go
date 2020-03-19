package sensugo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/influxdata/kapacitor/alert"
	"github.com/influxdata/kapacitor/keyvalue"
)

type Diagnostic interface {
	WithContext(ctx ...keyvalue.T) Diagnostic
	Error(msg string, err error, kvs ...keyvalue.T)
}

type Service struct {
	configValue atomic.Value
	diag        Diagnostic
}

func NewService(c Config, d Diagnostic) *Service {
	s := &Service{
		diag: d,
	}
	s.configValue.Store(c)
	return s
}

func (s *Service) Open() error {
	return nil
}

func (s *Service) Close() error {
	return nil
}

func (s *Service) config() Config {
	return s.configValue.Load().(Config)
}

func (s *Service) Update(newConfig []interface{}) error {
	if l := len(newConfig); l != 1 {
		return fmt.Errorf("expected only one new config object, got %d", l)
	}
	if c, ok := newConfig[0].(Config); !ok {
		return fmt.Errorf("expected config object to be of type %T, got %T", c, newConfig[0])
	} else {
		s.configValue.Store(c)
	}
	return nil
}

type testOptions struct {
	Check     string            `json:"check"`
	Entity    string            `json:"entity"`
	EntityTag string            `json:"entity_tag"`
	Message   string            `json:"message"`
	Namespace string            `json:"namespace"`
	Handlers  []string          `json:"handlers"`
	Labels    map[string]string `json:"labels"`
	Level     alert.Level       `json:"level"`
}

func (s *Service) TestOptions() interface{} {
	return &testOptions{
		Check:     "testName",
		Entity:    "testEntity",
		EntityTag: "testEntityTag",
		Message:   "testMessage",
		Namespace: "test",
		Handlers:  []string{},
		Labels:    make(map[string]string),
		Level:     alert.Critical,
	}
}

func (s *Service) Test(options interface{}) error {
	o, ok := options.(*testOptions)
	if !ok {
		return fmt.Errorf("unexpected options type %T", options)
	}
	return s.Alert(
		o.Check,
		o.Entity,
		o.Message,
		o.Namespace,
		o.Handlers,
		o.Labels,
		o.Level,
	)
}

func (s *Service) Alert(check, entity, message, namespace string, handlers []string, labels map[string]string, level alert.Level) error {
	c := s.config()

	if !c.Enabled {
		return errors.New("service is not enabled")
	}

	var status int
	switch level {
	case alert.OK:
		status = 0
	case alert.Info:
		status = 0
	case alert.Warning:
		status = 1
	case alert.Critical:
		status = 2
	default:
		status = 3
	}

	now := time.Now()

	event := &PostEvent{}

	event.Entity.EntityClass = "proxy"
	event.Entity.Metadata.Name = entity

	if namespace != "" {
		event.Entity.Metadata.Namespace = namespace
	} else {
		event.Entity.Metadata.Namespace = c.Namespace
	}
	event.Entity.Metadata.Labels = labels
	event.Check.Output = message
	event.Check.Status = status
	event.Check.Metadata.Name = check
	event.Check.Metadata.Labels = labels
	event.Check.Issued = now.Unix()
	event.Check.Executed = now.Unix()

	if len(handlers) > 0 {
		event.Check.Handlers = handlers
	} else {
		event.Check.Handlers = c.Handlers
	}

	data, err := json.Marshal(event)

	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", c.URL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create POST request: %v", err)
	}

	req.Header.Set("Authorization", c.Token)
	req.Header.Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(req.Context(), 1*time.Minute)
	defer cancel()
	req = req.WithContext(ctx)

	httpClient := &http.Client{}

	resp, err := httpClient.Do(req)

	if err != nil {
		s.diag.Error("Failed to POST to Sensu Go", err)
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		s.diag.Error(fmt.Sprintf("POST returned non 2xx status code (%d)", resp.StatusCode), err, keyvalue.KV("code", strconv.Itoa(resp.StatusCode)))
	}

	return nil
}

type HandlerConfig struct {
	// Sensu-go backend URL for which to post messages.
	// If empty uses the address from the configuration.
	URL string `mapstructure:"url"`

	Namespace string `mapstructure:"namespace"`

	// Metadata labels is a map of key value data to include on the sensu API request.
	Labels map[string]string `mapstructure:"labels"`

	// Sensu handler list
	// If empty uses the handler list from the configuration
	Handlers []string `mapstructure:"handlers"`

	// Check name in metadata
	Check string `mapstructure:"check"`

	// Entity name in metadata
	Entity string `mapstructure:"entity"`

	// Tag containing entity name
	EntityTag string `mapstructure:"entity-tag"`
}

// Event that is sent over HTTP POST request to sensu-go backend
type PostEvent struct {
	Entity struct {
		EntityClass string `json:"entity_class"`
		Metadata    struct {
			Name      string            `json:"name"`
			Namespace string            `json:"namespace"`
			Labels    map[string]string `json:"labels"`
		} `json:"metadata"`
	} `json:"entity"`
	Check struct {
		Output   string `json:"output"`
		Status   int    `json:"status"`
		Metadata struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Issued   int64    `json:"issued"`
		Executed int64    `json:"executed"`
		Handlers []string `json:"handlers"`
	} `json:"check"`
}

type handler struct {
	s    *Service
	c    HandlerConfig
	diag Diagnostic
}

func (s *Service) Handler(c HandlerConfig, ctx ...keyvalue.T) (alert.Handler, error) {
	return &handler{
		s:    s,
		c:    c,
		diag: s.diag.WithContext(ctx...),
	}, nil
}

func (h *handler) Handle(event alert.Event) {
	entity := h.c.Entity

	if h.c.Entity == "" && h.c.EntityTag != "" {
		if e, ok := event.Data.Tags[h.c.EntityTag]; ok {
			entity = e
		}
	}

	if err := h.s.Alert(
		event.State.ID,
		entity,
		event.State.Message,
		h.c.Namespace,
		h.c.Handlers,
		h.c.Labels,
		event.State.Level,
	); err != nil {
		h.diag.Error("failed to send event to Sensu Go", err)
	}
}
