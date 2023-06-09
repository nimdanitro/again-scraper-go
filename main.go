package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/pflag"
	"go.uber.org/zap"
)

type indoorData struct {
	ExternalTemperatures []any     `json:"externalTemperatures"`
	Humidity             float64   `json:"humidity"`
	Installed            bool      `json:"installed"`
	Temperature          float64   `json:"temperature"`
	Timestamp            time.Time `json:"timestamp"`
	Values               []struct {
		Value     float64   `json:"value"`
		Unit      string    `json:"unit"`
		Timestamp time.Time `json:"timestamp"`
	} `json:"values"`
}

var (
	sensorIDs   map[string]string
	temperature *prometheus.GaugeVec
	humidity    *prometheus.GaugeVec
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func fetchData(ctx context.Context, log *zap.Logger, id string) (*indoorData, error) {

	log.Debug("fetching data for sensor", zap.String("sensorId", id))
	resp, err := http.Get(fmt.Sprintf("https://deployment.egain.io/api/indoor/%s", id))
	if err != nil {
		log.Error("error fetching sensor data", zap.Error(err))
		return nil, err
	}
	defer resp.Body.Close()

	var data indoorData
	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		log.Error("error decoding sensor data", zap.Error(err))
		return nil, err
	}

	return &data, nil
}

func main() {
	// Parse command line flags
	pflag.StringToStringVarP(&sensorIDs, "sensors", "s", map[string]string{}, "Comma-separated list of sensor IDs, location mappings (ID12312=foobar,ID1321231=foobarbaz)")
	pflag.Lookup("sensors").Value.Set(os.Getenv("SENSORS"))
	pflag.Parse()

	// Initialize logger
	logger, err := zap.NewDevelopment()
	if err != nil {
		panic(fmt.Sprintf("Failed to create logger: %v", err))
	}
	defer logger.Sync()

	if len(sensorIDs) == 0 {
		fmt.Println("Please specify a comma-separated list of sensor IDs with the --sensors flag")
		return
	}
	logger.Info("starting up", zap.String("version", version), zap.String("commit", commit), zap.String("buildDate", date))

	// Initialize metrics
	temperature = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "indoor_temperature",
		Help: "Indoor temperature in degrees Celsius",
	}, []string{"sensor", "location"})
	prometheus.MustRegister(temperature)

	humidity = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "indoor_humidity",
		Help: "Indoor relative humidity as a percentage",
	}, []string{"sensor", "location"})
	prometheus.MustRegister(humidity)

	// Create HTTP server
	srv := &http.Server{Addr: ":8080"}

	// Initialize context and cancel function for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown on SIGINT or SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("Received signal, shutting down", zap.String("signal", sig.String()))
		cancel()
		logger.Info("Shutting down server")
		if err := srv.Shutdown(ctx); err != nil {
			logger.Fatal("error stopping server")
		}
	}()

	// Start ticker to fetch data every 2 minutes
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	// Initialize wait group
	wg := &sync.WaitGroup{}

	// Start goroutine to fetch data for each sensor
	for s, l := range sensorIDs {
		wg.Add(1)
		go func(sensorID, location string) {
			defer wg.Done()

			logger.Info("starting fetching routine", zap.String("sensorId", sensorID), zap.String("location", location))
			data, err := fetchData(ctx, logger, sensorID)
			if err != nil {
				logger.Error("Failed to fetch data", zap.Error(err), zap.String("sensorID", sensorID), zap.String("location", location))
			}
			logger.Info("Fetched data",
				zap.Float64("temperature", data.Temperature),
				zap.Float64("humidity", data.Humidity),
				zap.String("sensorId", sensorID),
				zap.String("location", location),
				zap.Time("timestamp", data.Timestamp),
			)

			temperature.WithLabelValues(sensorID, location).Set(data.Temperature)
			humidity.WithLabelValues(sensorID, location).Set(data.Humidity)

			for {
				select {
				case <-ticker.C:
					data, err := fetchData(ctx, logger, sensorID)
					if err != nil {
						logger.Error("Failed to fetch data", zap.Error(err), zap.String("sensorID", sensorID), zap.String("location", location))
						continue
					}

					logger.Info("Fetched data",
						zap.Float64("temperature", data.Temperature),
						zap.Float64("humidity", data.Humidity),
						zap.String("sensorId", sensorID),
						zap.String("location", location),
						zap.Time("timestamp", data.Timestamp),
					)

					if data != nil && data.Timestamp.IsZero() {
						logger.Error("Bogus to data fetched with zero values, skipping update", 
							zap.Error(err), zap.String("sensorID", sensorID), zap.String("location", location),
						)
						continue
					}

					temperature.WithLabelValues(sensorID, location).Set(data.Temperature)
					humidity.WithLabelValues(sensorID, location).Set(data.Humidity)
				case <-ctx.Done():
					return
				}
			}
		}(s, l)
	}

	// Serve metrics endpoint
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	})

	logger.Info("Starting server")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("Failed to start server", zap.Error(err))
		cancel()
	}

	// Wait for shutdown
	wg.Wait()
}
