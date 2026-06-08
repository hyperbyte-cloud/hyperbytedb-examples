package main

import (
	"fmt"
	"strconv"
	"strings"
)

func escapeTagValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, " ", `\ `)
	s = strings.ReplaceAll(s, ",", `\,`)
	s = strings.ReplaceAll(s, "=", `\=`)
	return s
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

type GenerationPoint struct {
	SiteID         string
	SiteName       string
	Region         string
	Source         string
	PowerMW        float64
	CapacityMW     float64
	CapacityFactor float64
	TimestampNS    int64
}

func (p GenerationPoint) Line() string {
	return fmt.Sprintf(
		"energy_generation,site_id=%s,site_name=%s,region=%s,source=%s power_mw=%s,capacity_mw=%s,capacity_factor=%s %d",
		escapeTagValue(p.SiteID),
		escapeTagValue(p.SiteName),
		escapeTagValue(p.Region),
		escapeTagValue(p.Source),
		formatFloat(p.PowerMW),
		formatFloat(p.CapacityMW),
		formatFloat(p.CapacityFactor),
		p.TimestampNS,
	)
}

type DemandPoint struct {
	SiteID      string
	SiteName    string
	Region      string
	Sector      string
	DemandMW    float64
	PeakMW      float64
	TimestampNS int64
}

func (p DemandPoint) Line() string {
	return fmt.Sprintf(
		"energy_demand,site_id=%s,site_name=%s,region=%s,sector=%s demand_mw=%s,peak_mw=%s %d",
		escapeTagValue(p.SiteID),
		escapeTagValue(p.SiteName),
		escapeTagValue(p.Region),
		escapeTagValue(p.Sector),
		formatFloat(p.DemandMW),
		formatFloat(p.PeakMW),
		p.TimestampNS,
	)
}

type FlowPoint struct {
	FromSite    string
	ToSite      string
	Region      string
	FlowType    string
	Direction   string
	PowerMW     float64
	LossesMW    float64
	TimestampNS int64
}

func (p FlowPoint) Line() string {
	return fmt.Sprintf(
		"energy_flow,from_site=%s,to_site=%s,region=%s,flow_type=%s,direction=%s power_mw=%s,losses_mw=%s %d",
		escapeTagValue(p.FromSite),
		escapeTagValue(p.ToSite),
		escapeTagValue(p.Region),
		escapeTagValue(p.FlowType),
		escapeTagValue(p.Direction),
		formatFloat(p.PowerMW),
		formatFloat(p.LossesMW),
		p.TimestampNS,
	)
}

type StoragePoint struct {
	SiteID       string
	SiteName     string
	Region       string
	StorageType  string
	SOCPercent   float64
	ChargeMW     float64
	DischargeMW  float64
	CapacityMWh  float64
	EnergyMWh    float64
	TimestampNS  int64
}

func (p StoragePoint) Line() string {
	return fmt.Sprintf(
		"energy_storage,site_id=%s,site_name=%s,region=%s,storage_type=%s soc_percent=%s,charge_mw=%s,discharge_mw=%s,capacity_mwh=%s,energy_mwh=%s %d",
		escapeTagValue(p.SiteID),
		escapeTagValue(p.SiteName),
		escapeTagValue(p.Region),
		escapeTagValue(p.StorageType),
		formatFloat(p.SOCPercent),
		formatFloat(p.ChargeMW),
		formatFloat(p.DischargeMW),
		formatFloat(p.CapacityMWh),
		formatFloat(p.EnergyMWh),
		p.TimestampNS,
	)
}

type BalancePoint struct {
	Region       string
	GenerationMW float64
	DemandMW     float64
	StorageNetMW float64
	FlowNetMW    float64
	NetBalanceMW float64
	TimestampNS  int64
}

func (p BalancePoint) Line() string {
	return fmt.Sprintf(
		"energy_balance,region=%s generation_mw=%s,demand_mw=%s,storage_net_mw=%s,flow_net_mw=%s,net_balance_mw=%s %d",
		escapeTagValue(p.Region),
		formatFloat(p.GenerationMW),
		formatFloat(p.DemandMW),
		formatFloat(p.StorageNetMW),
		formatFloat(p.FlowNetMW),
		formatFloat(p.NetBalanceMW),
		p.TimestampNS,
	)
}

func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}
