package main

type GeneratorSite struct {
	ID       string
	Name     string
	Region   string
	Source   string
	Capacity float64
}

type DemandSite struct {
	ID     string
	Name   string
	Region string
	Sector string
	PeakMW float64
}

type StorageSite struct {
	ID           string
	Name         string
	Region       string
	StorageType  string
	CapacityMWh  float64
	MaxPowerMW   float64
}

var generatorSites = []GeneratorSite{
	{ID: "solar_mojave", Name: "Mojave Solar Park", Region: "us_west", Source: "solar", Capacity: 280},
	{ID: "wind_north_sea", Name: "North Sea Wind", Region: "eu_north", Source: "wind", Capacity: 420},
	{ID: "hydro_alps", Name: "Alpine Hydro", Region: "eu_central", Source: "hydro", Capacity: 350},
	{ID: "gas_texas_hub", Name: "Texas Gas Peaker", Region: "us_central", Source: "gas", Capacity: 180},
	{ID: "biomass_oregon", Name: "Oregon Biomass", Region: "us_west", Source: "biomass", Capacity: 65},
}

var demandSites = []DemandSite{
	{ID: "metro_la", Name: "Los Angeles Metro", Region: "us_west", Sector: "commercial", PeakMW: 320},
	{ID: "metro_nyc", Name: "New York Metro", Region: "us_east", Sector: "commercial", PeakMW: 410},
	{ID: "industrial_ruhr", Name: "Ruhr Industrial", Region: "eu_central", Sector: "industrial", PeakMW: 290},
	{ID: "residential_oslo", Name: "Oslo Residential", Region: "eu_north", Sector: "residential", PeakMW: 140},
}

var storageSites = []StorageSite{
	{ID: "battery_la_hub", Name: "LA Battery Hub", Region: "us_west", StorageType: "battery", CapacityMWh: 400, MaxPowerMW: 100},
	{ID: "pumped_alps", Name: "Alpine Pumped Hydro", Region: "eu_central", StorageType: "pumped_hydro", CapacityMWh: 1200, MaxPowerMW: 200},
}
