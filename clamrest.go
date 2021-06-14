package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dutchcoders/go-clamd"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var opts map[string]string

func init() {
	log.SetOutput(ioutil.Discard)
}

type Error struct {
	Error string `json:"Error"`
}

func writeError(w http.ResponseWriter, statusCode int, err string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)

	errJson, _ := json.Marshal(&Error{err})
	if errJson != nil {
		fmt.Fprint(w, string(errJson))
	}
}

func home(w http.ResponseWriter, r *http.Request) {
	c := clamd.NewClamd(opts["CLAMD_PORT"])

	response, err := c.Stats()

	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not get stats: "+err.Error())
		return
	}

	resJson, eRes := json.Marshal(response)
	if eRes != nil {
		writeError(w, http.StatusInternalServerError, "Could not marshal JSON: "+eRes.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	fmt.Fprint(w, string(resJson))
}

func scanPathHandler(w http.ResponseWriter, r *http.Request) {
	paths, ok := r.URL.Query()["path"]
	if !ok || len(paths[0]) < 1 {
		log.Println("Url Param 'path' is missing")
		return
	}

	path := paths[0]

	c := clamd.NewClamd(opts["CLAMD_PORT"])
	response, err := c.AllMatchScanFile(path)

	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not scan file: "+err.Error())
		return
	}

	var scanResults []*clamd.ScanResult

	for responseItem := range response {
		scanResults = append(scanResults, responseItem)
	}

	resJson, eRes := json.Marshal(scanResults)
	if eRes != nil {
		fmt.Println(eRes)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	fmt.Fprint(w, string(resJson))
}

//This is where the action happens.
func scanHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	//POST takes the uploaded file(s) and saves it to disk.
	case "POST":
		c := clamd.NewClamd(opts["CLAMD_PORT"])
		//get the multipart reader for the request.
		reader, err := r.MultipartReader()

		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not initialize reader: "+err.Error())
			return
		}

		part, err := reader.NextPart()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not read file: "+err.Error())
			return
		}

		//if part.FileName() is empty, skip this iteration.
		if part.FileName() == "" {
			writeError(w, http.StatusBadRequest, "Filename is empty")
			return
		}

		fmt.Printf(time.Now().Format(time.RFC3339) + " Started scanning: " + part.FileName() + "\n")
		var abort chan bool
		response, err := c.ScanStream(part, abort)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not scan file: "+err.Error())
			return
		}

		s := <-response

		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		respJson, err := json.Marshal(&s)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not marshal JSON: "+err.Error())
			return
		}

		switch s.Status {
		case clamd.RES_OK:
			w.WriteHeader(http.StatusOK)
		case clamd.RES_FOUND:
			w.WriteHeader(http.StatusNotAcceptable)
		case clamd.RES_ERROR:
			w.WriteHeader(http.StatusBadRequest)
		case clamd.RES_PARSE_ERROR:
			w.WriteHeader(http.StatusPreconditionFailed)
		default:
			w.WriteHeader(http.StatusNotImplemented)
		}

		fmt.Fprint(w, string(respJson))
		fmt.Printf(time.Now().Format(time.RFC3339)+" Scan result for: %v, %v\n", part.FileName(), s)
		fmt.Printf(time.Now().Format(time.RFC3339) + " Finished scanning: " + part.FileName() + "\n")
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func waitForClamD(port string, times int) {
	clamdTest := clamd.NewClamd(port)
	clamdTest.Ping()
	version, err := clamdTest.Version()

	if err != nil {
		if times < 30 {
			fmt.Printf("clamD not running, waiting times [%v]\n", times)
			time.Sleep(time.Second * 4)
			waitForClamD(port, times+1)
		} else {
			fmt.Printf("Error getting clamd version: %v\n", err)
			os.Exit(1)
		}
	} else {
		for version_string := range version {
			fmt.Printf("Clamd version: %#v\n", version_string.Raw)
		}
	}
}

func main() {

	const (
		PORT     = ":9000"
		SSL_PORT = ":9443"
	)

	opts = make(map[string]string)

	for _, e := range os.Environ() {
		pair := strings.Split(e, "=")
		opts[pair[0]] = pair[1]
	}

	if opts["CLAMD_PORT"] == "" {
		opts["CLAMD_PORT"] = "tcp://localhost:3310"
	}

	fmt.Printf("Starting clamav rest bridge\n")
	fmt.Printf("Connecting to clamd on %v\n", opts["CLAMD_PORT"])
	waitForClamD(opts["CLAMD_PORT"], 1)

	fmt.Printf("Connected to clamd on %v\n", opts["CLAMD_PORT"])

	http.HandleFunc("/scan", scanHandler)
	http.HandleFunc("/scanPath", scanPathHandler)
	http.HandleFunc("/", home)

	// Prometheus metrics
	http.Handle("/metrics", promhttp.Handler())

	// Start the HTTPS server in a goroutine
	go http.ListenAndServeTLS(SSL_PORT, "/etc/ssl/clamav-rest/server.crt", "/etc/ssl/clamav-rest/server.key", nil)

	// Start the HTTP server
	http.ListenAndServe(PORT, nil)
}
