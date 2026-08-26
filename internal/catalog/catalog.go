// Package catalog models the silo and fermentation rule catalog. It maintains
// geometry, zones, compaction layers, plot and harvest-batch links, chopping,
// inoculant, film seals, the opening face, instrument capabilities and integer
// fixed-point thresholds. Snapshots are immutable and versioned: historical
// versions referenced by a locked task must never be modified in place.
package catalog

// CatalogSnapshot is the immutable, versioned snapshot frozen at task lock time.
type CatalogSnapshot struct {
	ID               string
	Version          int64
	Geometry         Geometry
	PlotID           string
	HarvestBatchID   string
	ChopLengthRule   ChopLengthRule
	Inoculant        InoculantSummary
	Zones            []Zone
	CompactionLayers []CompactionLayer
	Adjacency        map[string][]string
	OpenFace         OpenFace
	Instruments      []InstrumentCapability
	Thresholds       Thresholds
	Fermentation     FermentationRule
	Depths           []int // ordered coring depths, shallow-first
}

// SamplingPlan returns the three-dimensional sampling grid implied by the
// snapshot's zones, compaction layers and coring depths.
func (s CatalogSnapshot) SamplingPlan() SamplingPlan {
	return SamplingPlan{Zones: zoneIDs(s.Zones), Layers: layersByZone(s.CompactionLayers), Depths: append([]int(nil), s.Depths...)}
}

// SamplingPlan describes the grid required by a locked task.
type SamplingPlan struct {
	Zones  []string
	Layers map[string][]int
	Depths []int
}

func zoneIDs(zones []Zone) []string {
	out := make([]string, 0, len(zones))
	for _, z := range zones {
		out = append(out, z.ID)
	}
	return out
}

func layersByZone(layers []CompactionLayer) map[string][]int {
	out := make(map[string][]int)
	for _, l := range layers {
		out[l.ZoneID] = append(out[l.ZoneID], l.Seq)
	}
	return out
}

// Geometry summarizes the silo body used to validate sampling coordinates.
type Geometry struct {
	SiloID   string
	Capacity int64 // integer fixed-point, scaled
	Scale    int64
}

// Zone is one named silo zone; zones may be configured mutually exclusive.
type Zone struct {
	ID        string
	Exclusive bool
}

// CompactionLayer is one compaction layer within a zone, listed outer-first.
type CompactionLayer struct {
	ZoneID string
	Seq    int
}

// ChopLengthRule is the chopping length rule summary referenced by digest.
type ChopLengthRule struct {
	ID     string
	Digest string
	MinMM  int
	MaxMM  int
}

// InoculantSummary is the inoculant summary referenced by digest.
type InoculantSummary struct {
	Digest string
	Strain string
	Dose   int64 // integer fixed-point
	Scale  int64
}

// OpenFace is the expected opening face where ventilation is verified.
type OpenFace struct {
	ID         string
	Ventilator string
}

// InstrumentCapability describes one instrument and the metrics it can emit.
type InstrumentCapability struct {
	Type    InstrumentType
	Metrics []string
}

// InstrumentType enumerates the scriptable instruments.
type InstrumentType string

const (
	InstrumentPH       InstrumentType = "ph_meter"
	InstrumentNIR      InstrumentType = "nir"
	InstrumentChromato InstrumentType = "chromatograph"
	InstrumentGasProbe InstrumentType = "gas_probe"
)

// Thresholds holds the integer fixed-point safety and fermentation thresholds.
type Thresholds struct {
	OxygenMin       int64 // scaled
	HydrogenSulfMax int64 // scaled
	ButyricAcidMax  int64 // scaled
	AmmoniaNMax     int64 // scaled
	MycotoxinMax    int64 // scaled
	TempRiseMax     int64 // scaled
	Scale           int64
	MaxRetries      int
	MaxInterval     int64 // maximum logical-time gap between ventilation readings
}

// FermentationRule is the fermentation judgement rule summary.
type FermentationRule struct {
	ID     string
	Digest string
}

// Catalog provides read-only access to the versioned snapshots. Mutations are
// deliberately out of scope for this interface; callers build a new version.
type Catalog interface {
	// Snapshot returns the snapshot for the given id.
	Snapshot(id string) (CatalogSnapshot, error)
}
