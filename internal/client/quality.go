package client

import (
	"log"
	"sync"
	"time"
)

// ConnectionQuality tracks the quality of a network connection.
type ConnectionQuality struct {
	Latency       time.Duration
	PacketLoss    float64 // 0.0 - 1.0
	Jitter        time.Duration
	LastMeasured  time.Time
	Samples       int
	mu            sync.RWMutex
}

// QualityMonitor monitors connection quality for all networks.
type QualityMonitor struct {
	qualities map[string]*ConnectionQuality // peerID -> quality
	mu        sync.RWMutex
}

// NewQualityMonitor creates a new quality monitor.
func NewQualityMonitor() *QualityMonitor {
	return &QualityMonitor{
		qualities: make(map[string]*ConnectionQuality),
	}
}

// UpdateQuality updates the quality metrics for a peer.
func (qm *QualityMonitor) UpdateQuality(peerID string, latency time.Duration, packetLoss float64) {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	
	q, ok := qm.qualities[peerID]
	if !ok {
		q = &ConnectionQuality{
			Samples: 0,
		}
		qm.qualities[peerID] = q
	}
	
	q.mu.Lock()
	defer q.mu.Unlock()
	
	// Calculate jitter (latency variance)
	if q.Samples > 0 {
		latencyDiff := latency - q.Latency
		if latencyDiff < 0 {
			latencyDiff = -latencyDiff
		}
		q.Jitter = (q.Jitter*9 + latencyDiff) / 10 // Moving average
	}
	
	q.Latency = latency
	q.PacketLoss = packetLoss
	q.LastMeasured = time.Now()
	q.Samples++
}

// GetQuality returns the quality metrics for a peer.
func (qm *QualityMonitor) GetQuality(peerID string) *ConnectionQuality {
	qm.mu.RLock()
	defer qm.mu.RUnlock()
	
	q, ok := qm.qualities[peerID]
	if !ok {
		return nil
	}
	
	// Return a copy
	q.mu.RLock()
	defer q.mu.RUnlock()
	
	copy := &ConnectionQuality{
		Latency:      q.Latency,
		PacketLoss:   q.PacketLoss,
		Jitter:       q.Jitter,
		LastMeasured: q.LastMeasured,
		Samples:      q.Samples,
	}
	
	return copy
}

// IsHealthy checks if a connection is healthy based on quality metrics.
func (q *ConnectionQuality) IsHealthy() bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	
	// Unhealthy if:
	// - Latency > 500ms
	// - Packet loss > 30%
	// - No measurement in last 60 seconds
	if q.Latency > 500*time.Millisecond {
		return false
	}
	if q.PacketLoss > 0.3 {
		return false
	}
	if time.Since(q.LastMeasured) > 60*time.Second {
		return false
	}
	
	return true
}

// ShouldUseRelay determines if relay should be used instead of direct connection.
func (q *ConnectionQuality) ShouldUseRelay() bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	
	// Use relay if direct connection is poor
	if q.PacketLoss > 0.3 {
		return true
	}
	if q.Latency > 300*time.Millisecond && q.Jitter > 100*time.Millisecond {
		return true
	}
	
	return false
}

// MonitorAll checks quality for all peers and returns unhealthy ones.
func (qm *QualityMonitor) MonitorAll() []string {
	qm.mu.RLock()
	defer qm.mu.RUnlock()
	
	var unhealthy []string
	for peerID, q := range qm.qualities {
		if !q.IsHealthy() {
			unhealthy = append(unhealthy, peerID)
			log.Printf("[QualityMonitor] Peer %s unhealthy: latency=%v, loss=%.2f%%", 
				peerID, q.Latency, q.PacketLoss*100)
		}
	}
	
	return unhealthy
}

// GetStats returns overall quality statistics.
func (qm *QualityMonitor) GetStats() map[string]interface{} {
	qm.mu.RLock()
	defer qm.mu.RUnlock()
	
	stats := make(map[string]interface{})
	stats["total_peers"] = len(qm.qualities)
	
	var healthy, unhealthy int
	var avgLatency time.Duration
	var avgLoss float64
	
	for _, q := range qm.qualities {
		if q.IsHealthy() {
			healthy++
		} else {
			unhealthy++
		}
		avgLatency += q.Latency
		avgLoss += q.PacketLoss
	}
	
	if len(qm.qualities) > 0 {
		avgLatency /= time.Duration(len(qm.qualities))
		avgLoss /= float64(len(qm.qualities))
	}
	
	stats["healthy_peers"] = healthy
	stats["unhealthy_peers"] = unhealthy
	stats["avg_latency"] = avgLatency
	stats["avg_packet_loss"] = avgLoss
	
	return stats
}