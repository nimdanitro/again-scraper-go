package egain

import "time"

type Sensor struct {
	Location    string
	SensorID    string
	lastReading time.Time
}

type SensorReading struct {
	indoorData
	Sensor
}

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
