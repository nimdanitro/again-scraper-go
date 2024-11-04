package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nimdanitro/again-scraper-go/pkg/egain"
	"github.com/spf13/pflag"
	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.20.0"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	sensorIDs map[string]string
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Parse command line flags
	pflag.StringToStringVarP(&sensorIDs, "sensors", "s", map[string]string{}, "Comma-separated list of sensor IDs, location mappings (ID12312=foobar,ID1321231=foobarbaz)")
	pflag.Lookup("sensors").Value.Set(os.Getenv("SENSORS"))
	pflag.Parse()

	// Setup Otel
	shutdown, err := setupOTelSDK(ctx)
	defer shutdown(ctx)
	if err != nil {
		panic(err)
	}

	// Initialize logger
	core := zapcore.NewTee(
		zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(os.Stdout), zapcore.DebugLevel),
		otelzap.NewCore("github.com/nimdanitro/again-scraper-go", otelzap.WithLoggerProvider(global.GetLoggerProvider())),
	)
	logger := zap.New(core)
	defer logger.Sync()
	logger.Info("starting up", zap.String("version", version), zap.String("commit", commit), zap.String("buildDate", date))

	if len(sensorIDs) == 0 {
		fmt.Println("Please specify a comma-separated list of sensor IDs with the --sensors flag")
		return
	}

	// Initialize metrics
	meter := otel.Meter(
		"gitub.com/nimdanitro/again-scraper-go",
		metric.WithInstrumentationAttributes(semconv.OTelScopeName("gitub.com/nimdanitro/again-scraper-go")),
	)
	temperature, _ := meter.Float64Gauge("sensor.temperature",
		metric.WithUnit("°C"),
		metric.WithDescription("Indoor temperature in degrees Celsius"),
	)

	humidity, _ := meter.Float64Gauge("sensor.humidity",
		metric.WithUnit("%rH"),
		metric.WithDescription("Indoor relative humidity as a percentage"),
	)

	lastReading, _ := meter.Float64Histogram(
		"sensor.lastReading.duration",
		metric.WithDescription("The duration since the last sensor reading."),
		metric.WithUnit("s"),
	)

	// Get the sensor configurations
	sensors := []egain.Sensor{}
	for s, l := range sensorIDs {
		sensors = append(sensors, egain.Sensor{SensorID: s, Location: l})
	}

	// create the fetcher
	client, err := egain.NewFetcher(egain.WithLogger(logger), egain.WithSensors(sensors))
	if err != nil {
		log.Fatal("cannot create fetcher", zap.Error(err))
	}

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	readSensors := func() {
		logger.Info("fetching data from egain")
		sensorReadings, err := client.Fetch(ctx)
		if err != nil {
			logger.Error("Failed to fetch data", zap.Error(err))
			return
		}

		for _, data := range sensorReadings {
			logger.Info("Fetched data",
				zap.Float64("temperature", data.Temperature),
				zap.Float64("humidity", data.Humidity),
				zap.String("sensorId", data.SensorID),
				zap.String("location", data.Location),
				zap.Time("timestamp", data.Timestamp),
			)
			temperature.Record(ctx, data.Temperature, metric.WithAttributes(
				attribute.String("sensor.id", data.SensorID),
				attribute.String("sensor.location", data.Location),
			))
			humidity.Record(ctx, data.Temperature, metric.WithAttributes(
				attribute.String("sensor.id", data.SensorID),
				attribute.String("sensor.location", data.Location),
			))
			lastReading.Record(ctx, time.Since(data.Timestamp).Seconds(), metric.WithAttributes(
				attribute.String("sensor.id", data.SensorID),
				attribute.String("sensor.location", data.Location),
			))
		}
	}

	readSensors()

	for {
		select {
		case <-ticker.C:
			readSensors()
		case <-ctx.Done():
			return
		}
	}
}
