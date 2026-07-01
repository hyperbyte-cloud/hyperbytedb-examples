package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

type InfluxWriter struct {
	URL      string
	Database string
	writeNum atomic.Int64
}

func (w *InfluxWriter) CreateDatabase() error {
	url := fmt.Sprintf("%s/query", w.URL)
	body := fmt.Sprintf("q=CREATE DATABASE %s", w.Database)
	resp, err := http.Post(url, "application/x-www-form-urlencoded", strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("create database request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create database failed (status %d): %s", resp.StatusCode, b)
	}
	return nil
}

func (w *InfluxWriter) WriteBody(body string) error {
	n := w.writeNum.Add(1)
	start := time.Now()
	url := fmt.Sprintf("%s/write?db=%s&precision=ns", w.URL, w.Database)
	resp, err := http.Post(url, "text/plain", strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("write request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("influx write failed (status %d): %s", resp.StatusCode, b)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		log.Printf("[write %d] SLOW: %v for %d bytes", n, d.Round(time.Millisecond), len(body))
	} else if n <= 3 {
		log.Printf("[write %d] OK: %v for %d bytes", n, d.Round(time.Millisecond), len(body))
	}
	return nil
}
