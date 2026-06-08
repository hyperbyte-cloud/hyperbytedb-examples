package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

type InfluxWriter struct {
	URL      string
	Database string
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
	if body == "" {
		return nil
	}
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
	return nil
}
