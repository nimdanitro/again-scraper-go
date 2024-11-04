package egain

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

type Fetcher interface {
	Fetch(ctx context.Context) ([]*SensorReading, error)
}

type Client struct {
	client  *http.Client
	limit   *rate.Limiter
	log     *zap.Logger
	sensors []Sensor
}

type Option func(c *Client) error

func NewFetcher(opts ...Option) (*Client, error) {
	c := &Client{
		log:    zap.L(),
		limit:  rate.NewLimiter(rate.Every(5*time.Second), 4),
		client: &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
	}

	// apply the options
	for _, o := range opts {
		err := o(c)
		if err != nil {
			return nil, err
		}
	}

	return c, nil
}
func WithSensors(s []Sensor) Option {
	return func(c *Client) error {
		c.sensors = s
		return nil
	}
}

func WithLogger(l *zap.Logger) Option {
	return func(c *Client) error {
		c.log = l
		return nil
	}
}

func (c *Client) fetchSensorData(ctx context.Context, s *Sensor) (*SensorReading, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	c.log.Debug("fetching data for sensor", zap.String("sensorId", s.SensorID))
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://deployment.egain.io/api/indoor/%s", s.SensorID), nil)
	if err != nil {
		c.log.Error("cannot create request", zap.Error(err))
		return nil, err
	}

	// apply the ratelimit
	err = c.limit.Wait(ctx)
	if err != nil {
		c.log.Error("cannot await rate limit", zap.Error(err))
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		c.log.Error("error fetching sensor data", zap.Error(err))
		return nil, err
	}
	defer resp.Body.Close()

	var data indoorData
	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		c.log.Error("error decoding sensor data", zap.Error(err))
		return nil, err
	}

	return &SensorReading{indoorData: data, Sensor: *s}, nil
}

func (c *Client) Fetch(ctx context.Context) (r []*SensorReading, err error) {
	for _, sensor := range c.sensors {
		reading, err := c.fetchSensorData(ctx, &sensor)
		if err != nil {
			c.log.Error("cannot fetch sensor measurements",
				zap.String("sensorID", sensor.SensorID),
				zap.String("location", sensor.Location),
				zap.Error(err),
			)
			continue
		}
		r = append(r, reading)
	}

	return r, nil
}
