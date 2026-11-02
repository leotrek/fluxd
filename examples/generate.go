// Command generate writes deterministic optical and thermal replay fixtures.
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"fluxd/internal/metadata"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	for _, sensor := range []string{"optical", "thermal"} {
		m := metadata.Record{SchemaVersion: 1, ID: sensor + "-0001", SensorType: sensor, Timestamp: time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC), Width: 128, Height: 64, Bands: 4, SampleBitDepth: 12, StorageBitDepth: 16, SampleType: "uint", ByteOrder: "little", Layout: "BIL"}
		f, e := os.Create(sensor + ".raw")
		if e != nil {
			return e
		}
		var p [2]byte
		for i := 0; i < 128*64*4; i++ {
			v := (i*17 + i/128*31) % 4096
			if sensor == "thermal" {
				v = 1800 + (i/128+i%128)%300
			}
			binary.LittleEndian.PutUint16(p[:], uint16(v))
			if _, e = f.Write(p[:]); e != nil {
				f.Close()
				return e
			}
		}
		if e = f.Close(); e != nil {
			return e
		}
		b, e := json.MarshalIndent(m, "", "  ")
		if e != nil {
			return e
		}
		if e = os.WriteFile(sensor+".json", b, 0600); e != nil {
			return e
		}
	}
	return nil
}
