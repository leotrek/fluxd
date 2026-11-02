package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"fluxd/internal/transport"
)

func main() {
	socket := flag.String("socket", "./fluxd.sock", "Unix socket")
	op := flag.String("op", "ListAcquisitions", "ListAcquisitions, GetAcquisition or GetStatus")
	id := flag.String("id", "", "acquisition ID")
	offset := flag.Int("offset", 0, "list offset")
	flag.Parse()
	if *op != "ListAcquisitions" && *op != "GetAcquisition" && *op != "GetStatus" {
		fmt.Fprintln(os.Stderr, "payload access belongs to the external application; see docs/api.md")
		os.Exit(1)
	}
	c, e := transport.Dial(*socket)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	defer c.Close()
	r, e := transport.Call(c, transport.Request{Op: *op, ID: *id, Offset: *offset})
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	if e = json.NewEncoder(os.Stdout).Encode(r); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
