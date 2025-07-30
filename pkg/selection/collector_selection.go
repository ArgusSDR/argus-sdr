package selection

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"

	"argus-sdr/pkg/logger"
)

// CollectorMetrics holds performance metrics for a collector
type CollectorMetrics struct {
	StationID          string    `json:"station_id"`
	LastSeen           time.Time `json:"last_seen"`
	ResponseTime       float64   `json:"response_time_ms"` 
	SuccessRate        float64   `json:"success_rate"`        // 0.0 to 1.0
	ActiveRequests     int       `json:"active_requests"`
	TotalRequests      int       `json:"total_requests"`
	FailedRequests     int       `json:"failed_requests"`
	AverageFileSize    int64     `json:"average_file_size"`
	LastResponseTime   time.Time `json:"last_response_time"`
	ConnectionQuality  float64   `json:"connection_quality"`  // 0.0 to 1.0
	CPULoad           float64   `json:"cpu_load"`           // 0.0 to 1.0
	MemoryUsage       float64   `json:"memory_usage"`       // 0.0 to 1.0
	DiskSpace         float64   `json:"disk_space"`         // 0.0 to 1.0 (available)
	GeoLocation       GeoLocation `json:"geo_location"`
}

// GeoLocation represents collector geographic location
type GeoLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Region    string  `json:"region"`
	Timezone  string  `json:"timezone"`
}

// RequestRequirements defines requirements for a data collection request
type RequestRequirements struct {
	PreferredRegion    string        `json:"preferred_region"`
	MaxResponseTime    time.Duration `json:"max_response_time"`
	MinSuccessRate     float64       `json:"min_success_rate"`
	MaxConcurrentReqs  int          `json:"max_concurrent_requests"`
	RequiredDiskSpace  int64        `json:"required_disk_space"`
	PreferLowLatency   bool         `json:"prefer_low_latency"`
	PreferHighCapacity bool         `json:"prefer_high_capacity"`
	ExcludeStations    []string     `json:"exclude_stations"`
}

// SelectionStrategy defines different collector selection strategies
type SelectionStrategy int

const (
	StrategyRoundRobin SelectionStrategy = iota
	StrategyLeastLoaded
	StrategyBestPerformance
	StrategyGeographic
	StrategyWeightedRandom
	StrategyLoadBalanced
)

// CollectorSelector implements advanced collector selection algorithms
type CollectorSelector struct {
	log      *logger.Logger
	strategy SelectionStrategy
	metrics  map[string]*CollectorMetrics
	rand     *rand.Rand
}

// NewCollectorSelector creates a new collector selector
func NewCollectorSelector(log *logger.Logger, strategy SelectionStrategy) *CollectorSelector {
	return &CollectorSelector{
		log:      log,
		strategy: strategy,
		metrics:  make(map[string]*CollectorMetrics),
		rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// UpdateMetrics updates metrics for a collector
func (cs *CollectorSelector) UpdateMetrics(stationID string, metrics *CollectorMetrics) {
	metrics.StationID = stationID
	cs.metrics[stationID] = metrics
	cs.log.Debug("Updated metrics for collector %s: success_rate=%.3f, response_time=%.1fms, active_requests=%d", 
		stationID, metrics.SuccessRate, metrics.ResponseTime, metrics.ActiveRequests)
}

// SelectCollectors selects the best collectors for a request
func (cs *CollectorSelector) SelectCollectors(availableStations []string, requirements RequestRequirements, maxCollectors int) ([]string, error) {
	if len(availableStations) == 0 {
		return nil, fmt.Errorf("no collectors available")
	}

	// Filter stations based on requirements
	candidates := cs.filterCandidates(availableStations, requirements)
	if len(candidates) == 0 {
		cs.log.Warn("No collectors meet requirements, falling back to available stations")
		candidates = availableStations
	}

	// Apply selection strategy
	selected, err := cs.applyStrategy(candidates, requirements, maxCollectors)
	if err != nil {
		return nil, err
	}

	cs.log.Info("Selected %d collectors using %s strategy: %v", 
		len(selected), cs.getStrategyName(), selected)
	
	return selected, nil
}

// filterCandidates filters collectors based on requirements
func (cs *CollectorSelector) filterCandidates(stations []string, req RequestRequirements) []string {
	var candidates []string
	
	for _, stationID := range stations {
		metrics, exists := cs.metrics[stationID]
		if !exists {
			// No metrics available, include by default
			candidates = append(candidates, stationID)
			continue
		}

		// Check if station meets requirements
		if cs.meetsRequirements(metrics, req) {
			candidates = append(candidates, stationID)
		} else {
			cs.log.Debug("Collector %s filtered out: doesn't meet requirements", stationID)
		}
	}
	
	return candidates
}

// meetsRequirements checks if a collector meets the specified requirements
func (cs *CollectorSelector) meetsRequirements(metrics *CollectorMetrics, req RequestRequirements) bool {
	// Check excluded stations
	for _, excluded := range req.ExcludeStations {
		if metrics.StationID == excluded {
			return false
		}
	}

	// Check success rate
	if req.MinSuccessRate > 0 && metrics.SuccessRate < req.MinSuccessRate {
		return false
	}

	// Check response time
	if req.MaxResponseTime > 0 && time.Duration(metrics.ResponseTime)*time.Millisecond > req.MaxResponseTime {
		return false
	}

	// Check concurrent requests
	if req.MaxConcurrentReqs > 0 && metrics.ActiveRequests >= req.MaxConcurrentReqs {
		return false
	}

	// Check disk space (simplified - assume we need at least the required amount)
	if req.RequiredDiskSpace > 0 && metrics.DiskSpace < 0.1 { // Less than 10% disk space available
		return false
	}

	// Check region preference
	if req.PreferredRegion != "" && metrics.GeoLocation.Region != req.PreferredRegion {
		// Don't exclude, but will be deprioritized in selection
	}

	return true
}

// applyStrategy applies the selected strategy to choose collectors
func (cs *CollectorSelector) applyStrategy(candidates []string, req RequestRequirements, maxCollectors int) ([]string, error) {
	switch cs.strategy {
	case StrategyRoundRobin:
		return cs.selectRoundRobin(candidates, maxCollectors), nil
	case StrategyLeastLoaded:
		return cs.selectLeastLoaded(candidates, maxCollectors), nil
	case StrategyBestPerformance:
		return cs.selectBestPerformance(candidates, maxCollectors), nil
	case StrategyGeographic:
		return cs.selectGeographic(candidates, req, maxCollectors), nil
	case StrategyWeightedRandom:
		return cs.selectWeightedRandom(candidates, maxCollectors), nil
	case StrategyLoadBalanced:
		return cs.selectLoadBalanced(candidates, req, maxCollectors), nil
	default:
		return cs.selectRoundRobin(candidates, maxCollectors), nil
	}
}

// selectRoundRobin implements round-robin selection
func (cs *CollectorSelector) selectRoundRobin(candidates []string, maxCollectors int) []string {
	if len(candidates) <= maxCollectors {
		return candidates
	}
	
	// Simple round-robin based on current time
	start := int(time.Now().Unix()) % len(candidates)
	selected := make([]string, 0, maxCollectors)
	
	for i := 0; i < maxCollectors; i++ {
		idx := (start + i) % len(candidates)
		selected = append(selected, candidates[idx])
	}
	
	return selected
}

// selectLeastLoaded selects collectors with the lowest load
func (cs *CollectorSelector) selectLeastLoaded(candidates []string, maxCollectors int) []string {
	type collectorLoad struct {
		stationID string
		load      float64
	}
	
	var loads []collectorLoad
	for _, stationID := range candidates {
		metrics, exists := cs.metrics[stationID]
		load := 0.0
		if exists {
			// Calculate composite load score
			load = float64(metrics.ActiveRequests)*0.4 + 
				   metrics.CPULoad*0.3 + 
				   metrics.MemoryUsage*0.2 + 
				   (1.0-metrics.DiskSpace)*0.1
		}
		loads = append(loads, collectorLoad{stationID, load})
	}
	
	// Sort by load (ascending)
	sort.Slice(loads, func(i, j int) bool {
		return loads[i].load < loads[j].load
	})
	
	selected := make([]string, 0, maxCollectors)
	for i := 0; i < maxCollectors && i < len(loads); i++ {
		selected = append(selected, loads[i].stationID)
	}
	
	return selected
}

// selectBestPerformance selects collectors with the best performance metrics
func (cs *CollectorSelector) selectBestPerformance(candidates []string, maxCollectors int) []string {
	type collectorScore struct {
		stationID string
		score     float64
	}
	
	var scores []collectorScore
	for _, stationID := range candidates {
		metrics, exists := cs.metrics[stationID]
		score := 0.5 // Default score for collectors without metrics
		if exists {
			// Calculate composite performance score (0.0 to 1.0, higher is better)
			responseScore := math.Max(0, 1.0-metrics.ResponseTime/1000.0) // Normalize to 1 second
			successScore := metrics.SuccessRate
			qualityScore := metrics.ConnectionQuality
			loadScore := 1.0 - (float64(metrics.ActiveRequests)/10.0) // Assume 10 is high load
			
			score = responseScore*0.3 + successScore*0.4 + qualityScore*0.2 + loadScore*0.1
		}
		scores = append(scores, collectorScore{stationID, score})
	}
	
	// Sort by score (descending)
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})
	
	selected := make([]string, 0, maxCollectors)
	for i := 0; i < maxCollectors && i < len(scores); i++ {
		selected = append(selected, scores[i].stationID)
	}
	
	return selected
}

// selectGeographic selects collectors based on geographic preferences
func (cs *CollectorSelector) selectGeographic(candidates []string, req RequestRequirements, maxCollectors int) []string {
	if req.PreferredRegion == "" {
		// No geographic preference, fall back to round-robin
		return cs.selectRoundRobin(candidates, maxCollectors)
	}
	
	var preferred, others []string
	for _, stationID := range candidates {
		metrics, exists := cs.metrics[stationID]
		if exists && metrics.GeoLocation.Region == req.PreferredRegion {
			preferred = append(preferred, stationID)
		} else {
			others = append(others, stationID)
		}
	}
	
	// Prefer collectors in the preferred region
	selected := make([]string, 0, maxCollectors)
	
	// Add preferred region collectors first
	for i := 0; i < len(preferred) && len(selected) < maxCollectors; i++ {
		selected = append(selected, preferred[i])
	}
	
	// Fill remaining slots with other collectors
	for i := 0; i < len(others) && len(selected) < maxCollectors; i++ {
		selected = append(selected, others[i])
	}
	
	return selected
}

// selectWeightedRandom implements weighted random selection based on performance
func (cs *CollectorSelector) selectWeightedRandom(candidates []string, maxCollectors int) []string {
	if len(candidates) <= maxCollectors {
		return candidates
	}
	
	// Calculate weights based on performance metrics
	weights := make([]float64, len(candidates))
	totalWeight := 0.0
	
	for i, stationID := range candidates {
		metrics, exists := cs.metrics[stationID]
		weight := 1.0 // Default weight
		if exists {
			// Higher weight for better performing collectors
			weight = metrics.SuccessRate*0.5 + metrics.ConnectionQuality*0.3 + (1.0-float64(metrics.ActiveRequests)/10.0)*0.2
			weight = math.Max(0.1, weight) // Minimum weight
		}
		weights[i] = weight
		totalWeight += weight
	}
	
	selected := make([]string, 0, maxCollectors)
	remaining := make([]string, len(candidates))
	copy(remaining, candidates)
	remainingWeights := make([]float64, len(weights))
	copy(remainingWeights, weights)
	
	for len(selected) < maxCollectors && len(remaining) > 0 {
		// Select randomly based on weights
		r := cs.rand.Float64() * totalWeight
		cumulative := 0.0
		selectedIdx := 0
		
		for i, weight := range remainingWeights {
			cumulative += weight
			if r <= cumulative {
				selectedIdx = i
				break
			}
		}
		
		selected = append(selected, remaining[selectedIdx])
		
		// Remove selected item from remaining
		totalWeight -= remainingWeights[selectedIdx]
		remaining = append(remaining[:selectedIdx], remaining[selectedIdx+1:]...)
		remainingWeights = append(remainingWeights[:selectedIdx], remainingWeights[selectedIdx+1:]...)
	}
	
	return selected
}

// selectLoadBalanced implements a sophisticated load balancing algorithm
func (cs *CollectorSelector) selectLoadBalanced(candidates []string, req RequestRequirements, maxCollectors int) []string {
	type collectorRank struct {
		stationID string
		rank      float64
	}
	
	var ranks []collectorRank
	for _, stationID := range candidates {
		metrics, exists := cs.metrics[stationID]
		rank := 0.5 // Default rank
		if exists {
			// Calculate comprehensive ranking score
			performanceScore := metrics.SuccessRate*0.3 + 
							   math.Max(0, 1.0-metrics.ResponseTime/1000.0)*0.2 + 
							   metrics.ConnectionQuality*0.2
			
			loadScore := 1.0 - (float64(metrics.ActiveRequests)/10.0 + 
							   metrics.CPULoad*0.5 + 
							   metrics.MemoryUsage*0.3)
			
			resourceScore := metrics.DiskSpace*0.5 + (1.0-metrics.MemoryUsage)*0.5
			
			// Regional preference bonus
			regionBonus := 0.0
			if req.PreferredRegion != "" && metrics.GeoLocation.Region == req.PreferredRegion {
				regionBonus = 0.1
			}
			
			// Latency preference
			latencyBonus := 0.0
			if req.PreferLowLatency && metrics.ResponseTime < 100 {
				latencyBonus = 0.05
			}
			
			rank = performanceScore*0.4 + loadScore*0.4 + resourceScore*0.2 + regionBonus + latencyBonus
			rank = math.Max(0.0, math.Min(1.0, rank))
		}
		
		ranks = append(ranks, collectorRank{stationID, rank})
	}
	
	// Sort by rank (descending)
	sort.Slice(ranks, func(i, j int) bool {
		return ranks[i].rank > ranks[j].rank
	})
	
	selected := make([]string, 0, maxCollectors)
	for i := 0; i < maxCollectors && i < len(ranks); i++ {
		selected = append(selected, ranks[i].stationID)
	}
	
	return selected
}

// getStrategyName returns the human-readable name of the strategy
func (cs *CollectorSelector) getStrategyName() string {
	switch cs.strategy {
	case StrategyRoundRobin:
		return "round-robin"
	case StrategyLeastLoaded:
		return "least-loaded"
	case StrategyBestPerformance:
		return "best-performance"
	case StrategyGeographic:
		return "geographic"
	case StrategyWeightedRandom:
		return "weighted-random"
	case StrategyLoadBalanced:
		return "load-balanced"
	default:
		return "unknown"
	}
}

// GetMetrics returns current metrics for all collectors
func (cs *CollectorSelector) GetMetrics() map[string]*CollectorMetrics {
	result := make(map[string]*CollectorMetrics)
	for k, v := range cs.metrics {
		result[k] = v
	}
	return result
}

// GetDefaultRequirements returns default request requirements
func GetDefaultRequirements() RequestRequirements {
	return RequestRequirements{
		MaxResponseTime:   30 * time.Second,
		MinSuccessRate:    0.8,
		MaxConcurrentReqs: 5,
		PreferLowLatency:  true,
	}
}