package main

import (
	"math"
	"math/rand"
	"time"
)

type Simulator struct {
	rng       *rand.Rand
	batterySOC map[string]float64
	windState  map[string]float64
}

func NewSimulator(seed int64) *Simulator {
	s := &Simulator{
		rng:        rand.New(rand.NewSource(seed)),
		batterySOC: make(map[string]float64),
		windState:  make(map[string]float64),
	}
	for _, site := range storageSites {
		s.batterySOC[site.ID] = 45 + s.rng.Float64()*20
	}
	for _, site := range generatorSites {
		if site.Source == "wind" {
			s.windState[site.ID] = 0.45 + s.rng.Float64()*0.2
		}
	}
	return s
}

type CycleBatch struct {
	Generation []GenerationPoint
	Demand     []DemandPoint
	Flow       []FlowPoint
	Storage    []StoragePoint
	Balance    []BalancePoint
}

func (s *Simulator) GenerateCycle(now time.Time) CycleBatch {
	ts := now.Truncate(time.Minute).UnixNano()
	hour := float64(now.Hour()) + float64(now.Minute())/60.0

	genByRegion := map[string]float64{}
	demandByRegion := map[string]float64{}

	batch := CycleBatch{}

	for _, site := range generatorSites {
		power, factor := s.generatePower(site, hour)
		genByRegion[site.Region] += power
		batch.Generation = append(batch.Generation, GenerationPoint{
			SiteID:         site.ID,
			SiteName:       site.Name,
			Region:         site.Region,
			Source:         site.Source,
			PowerMW:        power,
			CapacityMW:     site.Capacity,
			CapacityFactor: factor,
			TimestampNS:    ts,
		})
	}

	for _, site := range demandSites {
		demand := s.generateDemand(site, hour)
		demandByRegion[site.Region] += demand
		batch.Demand = append(batch.Demand, DemandPoint{
			SiteID:      site.ID,
			SiteName:    site.Name,
			Region:      site.Region,
			Sector:      site.Sector,
			DemandMW:    demand,
			PeakMW:      site.PeakMW,
			TimestampNS: ts,
		})
	}

	storageNetByRegion := map[string]float64{}
	for _, site := range storageSites {
		soc := s.batterySOC[site.ID]
		net := genByRegion[site.Region] - demandByRegion[site.Region]
		charge, discharge := s.storageAction(site, soc, net)

		energyMWh := soc / 100 * site.CapacityMWh
		if charge > 0 {
			energyMWh += charge / 60
			soc = math.Min(95, soc+charge/site.MaxPowerMW*2.5)
		}
		if discharge > 0 {
			energyMWh -= discharge / 60
			soc = math.Max(10, soc-discharge/site.MaxPowerMW*2.5)
		}
		s.batterySOC[site.ID] = soc
		storageNetByRegion[site.Region] += discharge - charge

		batch.Storage = append(batch.Storage, StoragePoint{
			SiteID:      site.ID,
			SiteName:    site.Name,
			Region:      site.Region,
			StorageType: site.StorageType,
			SOCPercent:  soc,
			ChargeMW:    charge,
			DischargeMW: discharge,
			CapacityMWh: site.CapacityMWh,
			EnergyMWh:   energyMWh,
			TimestampNS: ts,
		})
	}

	batch.Flow = append(batch.Flow, s.interRegionalFlows(ts, genByRegion, demandByRegion)...)

	flowNetByRegion := map[string]float64{}
	for _, flow := range batch.Flow {
		switch flow.Direction {
		case "export":
			flowNetByRegion[flow.Region] -= flow.PowerMW
		case "import":
			flowNetByRegion[flow.Region] += flow.PowerMW
		}
	}

	regions := []string{"us_west", "us_east", "us_central", "eu_north", "eu_central"}
	for _, region := range regions {
		gen := genByRegion[region]
		demand := demandByRegion[region]
		storageNet := storageNetByRegion[region]
		flowNet := flowNetByRegion[region]
		balance := gen + storageNet + flowNet - demand

		batch.Balance = append(batch.Balance, BalancePoint{
			Region:       region,
			GenerationMW: gen,
			DemandMW:     demand,
			StorageNetMW: storageNet,
			FlowNetMW:    flowNet,
			NetBalanceMW: balance,
			TimestampNS:  ts,
		})

		if balance < -5 {
			importMW := math.Min(-balance, 80+s.rng.Float64()*40)
			batch.Flow = append(batch.Flow, FlowPoint{
				FromSite:    "external_grid",
				ToSite:      region,
				Region:      region,
				FlowType:    "grid_interconnect",
				Direction:   "import",
				PowerMW:     importMW,
				LossesMW:    importMW * 0.02,
				TimestampNS: ts,
			})
		} else if balance > 5 {
			exportMW := math.Min(balance, 60+s.rng.Float64()*30)
			batch.Flow = append(batch.Flow, FlowPoint{
				FromSite:    region,
				ToSite:      "external_grid",
				Region:      region,
				FlowType:    "grid_interconnect",
				Direction:   "export",
				PowerMW:     exportMW,
				LossesMW:    exportMW * 0.015,
				TimestampNS: ts,
			})
		}
	}

	return batch
}

func (s *Simulator) generatePower(site GeneratorSite, hour float64) (float64, float64) {
	switch site.Source {
	case "solar":
		// Peak around 13:00, zero at night.
		daylight := math.Max(0, math.Sin((hour-6)*math.Pi/12))
		factor := daylight * (0.75 + s.rng.Float64()*0.2)
		return site.Capacity * factor, factor
	case "wind":
		state := s.windState[site.ID]
		state += (s.rng.Float64() - 0.5) * 0.08
		state = clamp(state, 0.1, 0.95)
		s.windState[site.ID] = state
		factor := state * (0.85 + s.rng.Float64()*0.1)
		return site.Capacity * factor, factor
	case "hydro":
		factor := 0.55 + 0.15*math.Sin(hour*math.Pi/12) + (s.rng.Float64()-0.5)*0.05
		factor = clamp(factor, 0.35, 0.85)
		return site.Capacity * factor, factor
	case "gas":
		// Peaker: ramps during demand peaks.
		peak := math.Max(
			math.Exp(-math.Pow(hour-8, 2)/8),
			math.Exp(-math.Pow(hour-19, 2)/10),
		)
		factor := peak * (0.4 + s.rng.Float64()*0.5)
		return site.Capacity * factor, factor
	case "biomass":
		factor := 0.7 + (s.rng.Float64()-0.5)*0.08
		return site.Capacity * factor, factor
	default:
		return 0, 0
	}
}

func (s *Simulator) generateDemand(site DemandSite, hour float64) float64 {
	var profile float64
	switch site.Sector {
	case "residential":
		profile = 0.45 +
			0.35*math.Exp(-math.Pow(hour-7.5, 2)/4) +
			0.4*math.Exp(-math.Pow(hour-19, 2)/5)
	case "commercial":
		profile = 0.3 +
			0.55*math.Exp(-math.Pow(hour-14, 2)/18) +
			0.2*math.Exp(-math.Pow(hour-9, 2)/6)
	case "industrial":
		profile = 0.55 +
			0.35*math.Exp(-math.Pow(hour-11, 2)/30) +
			0.15*math.Exp(-math.Pow(hour-22, 2)/8)
	default:
		profile = 0.5
	}
	noise := 0.95 + s.rng.Float64()*0.1
	return site.PeakMW * clamp(profile*noise, 0.2, 1.0)
}

func (s *Simulator) storageAction(site StorageSite, soc, regionalNet float64) (charge, discharge float64) {
	if regionalNet > 8 && soc < 90 {
		charge = math.Min(site.MaxPowerMW, regionalNet*0.35)
		return charge, 0
	}
	if regionalNet < -8 && soc > 15 {
		discharge = math.Min(site.MaxPowerMW, -regionalNet*0.45)
		return 0, discharge
	}
	return 0, 0
}

func (s *Simulator) interRegionalFlows(ts int64, gen, demand map[string]float64) []FlowPoint {
	type link struct {
		from, to, region string
		capacity         float64
	}
	links := []link{
		{"us_west", "us_central", "us_west", 90},
		{"eu_north", "eu_central", "eu_north", 110},
		{"eu_central", "us_east", "eu_central", 70},
	}

	flows := make([]FlowPoint, 0, len(links))
	for _, l := range links {
		surplus := gen[l.from] - demand[l.from]
		if surplus <= 5 {
			continue
		}
		power := math.Min(surplus*0.5, l.capacity)
		if power < 1 {
			continue
		}
		flows = append(flows, FlowPoint{
			FromSite:    l.from,
			ToSite:      l.to,
			Region:      l.region,
			FlowType:    "transmission",
			Direction:   "internal",
			PowerMW:     power,
			LossesMW:    power * 0.03,
			TimestampNS: ts,
		})
	}
	return flows
}

func (b CycleBatch) Lines() []string {
	total := len(b.Generation) + len(b.Demand) + len(b.Flow) + len(b.Storage) + len(b.Balance)
	lines := make([]string, 0, total)
	for _, p := range b.Generation {
		lines = append(lines, p.Line())
	}
	for _, p := range b.Demand {
		lines = append(lines, p.Line())
	}
	for _, p := range b.Flow {
		lines = append(lines, p.Line())
	}
	for _, p := range b.Storage {
		lines = append(lines, p.Line())
	}
	for _, p := range b.Balance {
		lines = append(lines, p.Line())
	}
	return lines
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
