package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
)

const Namespace = "org.openegiz.team050"

type Node struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	Kind string  `json:"kind"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}
type Edge struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Length float64 `json:"length_km"`
}
type Route struct {
	ID    string   `json:"id"`
	Nodes []string `json:"nodes"`
}
type Config struct {
	Version           string  `json:"version"`
	TopologyVersion   string  `json:"topology_version"`
	LayoutVersion     string  `json:"layout_version"`
	ShiftSeconds      float64 `json:"shift_seconds"`
	TruckCount        int     `json:"truck_count"`
	Capacity          float64 `json:"capacity_t"`
	Body              float64 `json:"body_m3"`
	Bucket            float64 `json:"bucket_m3"`
	Fill              float64 `json:"bucket_fill"`
	Density           float64 `json:"density_t_m3"`
	PassSeconds       float64 `json:"pass_seconds"`
	PositionLoading   float64 `json:"position_loading_seconds"`
	PositionUnloading float64 `json:"position_unloading_seconds"`
	Unloading         float64 `json:"unloading_seconds"`
	SpeedLoaded       float64 `json:"speed_loaded_kmh"`
	SpeedEmpty        float64 `json:"speed_empty_kmh"`
	BufferCapacity    float64 `json:"buffer_capacity_t"`
	CrusherBase       float64 `json:"crusher_base_tph"`
	CrusherSwitch     float64 `json:"crusher_switch_seconds"`
	Nodes             []Node  `json:"nodes"`
	Edges             []Edge  `json:"edges"`
	Routes            []Route `json:"routes"`
	Hash              string  `json:"-"`
}

func LoadConfig(path string) (Config, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return Config{}, e
	}
	var c Config
	if e = json.Unmarshal(b, &c); e != nil {
		return c, e
	}
	h := sha256.Sum256(b)
	c.Hash = hex.EncodeToString(h[:])
	if c.TruckCount != 20 || c.ShiftSeconds <= 0 || c.PassSeconds <= 0 || c.Density <= 0 || c.Fill <= 0 || c.Fill > 1 || c.CrusherSwitch <= 0 || c.SpeedEmpty <= 0 || c.SpeedLoaded <= 0 || c.BufferCapacity < c.Capacity || c.Capacity <= 0 || c.Body <= 0 || c.Bucket <= 0 || c.CrusherBase <= 0 || c.PositionLoading <= 0 || c.PositionUnloading <= 0 || c.Unloading <= 0 {
		return c, fmt.Errorf("invalid equipment or timing configuration")
	}
	seen := map[string]bool{}
	for _, n := range c.Nodes {
		if seen[n.ID] || n.ID == "" {
			return c, fmt.Errorf("duplicate node")
		}
		seen[n.ID] = true
	}
	for _, id := range []string{"P", "E1", "E2", "E3", "E4", "C1", "C2", "C3", "R1", "R2"} {
		if !seen[id] {
			return c, fmt.Errorf("missing node %s", id)
		}
	}
	for _, e := range c.Edges {
		if e.Length <= 0 || !seen[e.From] || !seen[e.To] {
			return c, fmt.Errorf("invalid edge")
		}
	}
	for _, r := range c.Routes {
		if _, e := c.ValidatePath(r.Nodes); e != nil {
			return c, e
		}
	}
	return c, nil
}
func (c Config) EdgeLength(a, b string) float64 {
	for _, e := range c.Edges {
		if (e.From == a && e.To == b) || (e.From == b && e.To == a) {
			return e.Length
		}
	}
	return 0
}
func (c Config) Node(id string) Node {
	for _, n := range c.Nodes {
		if n.ID == id {
			return n
		}
	}
	return Node{}
}
func (c Config) ValidatePath(nodes []string) (float64, error) {
	if len(nodes) < 2 || len(nodes) > len(c.Nodes) {
		return 0, fmt.Errorf("Укажите связный путь от E до C")
	}
	if c.Node(nodes[0]).Kind != "loading" || c.Node(nodes[len(nodes)-1]).Kind != "crusher" {
		return 0, fmt.Errorf("Путь должен начинаться на погрузке E и завершаться на дробилке C")
	}
	seen := map[string]bool{}
	d := 0.0
	for i, n := range nodes {
		if seen[n] {
			return 0, fmt.Errorf("Повтор узла %s", n)
		}
		seen[n] = true
		if i > 0 {
			v := c.EdgeLength(nodes[i-1], n)
			if v <= 0 {
				return 0, fmt.Errorf("Нет дороги %s → %s", nodes[i-1], n)
			}
			d += v
		}
	}
	return d, nil
}

// ShortestPath uses the configured kilometre lengths, never drawing coordinates.
func (c Config) ShortestPath(a, b string) []string {
	d := map[string]float64{}
	prev := map[string]string{}
	done := map[string]bool{}
	for _, n := range c.Nodes {
		d[n.ID] = math.Inf(1)
	}
	d[a] = 0
	for range c.Nodes {
		u := ""
		best := math.Inf(1)
		for _, n := range c.Nodes {
			if !done[n.ID] && d[n.ID] < best {
				u = n.ID
				best = d[n.ID]
			}
		}
		if u == "" {
			break
		}
		if u == b {
			break
		}
		done[u] = true
		for _, e := range c.Edges {
			v := ""
			if e.From == u {
				v = e.To
			} else if e.To == u {
				v = e.From
			}
			if v != "" && d[u]+e.Length < d[v] {
				d[v] = d[u] + e.Length
				prev[v] = u
			}
		}
	}
	if math.IsInf(d[b], 1) {
		return nil
	}
	path := []string{b}
	for b != a {
		p, ok := prev[b]
		if !ok {
			return nil
		}
		b = p
		path = append([]string{b}, path...)
	}
	return path
}
func Reverse(p []string) []string {
	out := make([]string, len(p))
	for i, n := range p {
		out[len(p)-1-i] = n
	}
	return out
}
func ThingID(name string) string { return Namespace + ":" + name }
func Name(id string) string      { return strings.TrimPrefix(id, Namespace+":") }
