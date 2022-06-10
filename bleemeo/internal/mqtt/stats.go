package mqtt

import (
	"fmt"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

const (
	bucketSize   = 15 * time.Minute
	bucketMaxAge = 24 * time.Hour
)

// mqttStats contains statistics about the messages published to MQTT.
type mqttStats struct {
	l            sync.Mutex
	publishDates map[uint16]time.Time
	// Each bucket stores the average, minimum and maximum ack duration during bucketSize.
	buckets     map[time.Time]bucketStats
	globalStats bucketStats
}

type bucketStats struct {
	min, max, avg time.Duration
	nbMessages    int
}

func newMQTTStats() *mqttStats {
	return &mqttStats{
		publishDates: make(map[uint16]time.Time),
		buckets:      make(map[time.Time]bucketStats),
	}
}

// messagePublished records the time when a message was sent.
func (s *mqttStats) messagePublished(token paho.Token, now time.Time) {
	pubToken, ok := token.(*paho.PublishToken)
	if !ok {
		return
	}

	s.l.Lock()
	s.publishDates[pubToken.MessageID()] = now
	s.l.Unlock()
}

// ackReceived signals that a message was received and adds it to the statistics.
func (s *mqttStats) ackReceived(token paho.Token, now time.Time) {
	pubToken, ok := token.(*paho.PublishToken)
	if !ok {
		return
	}

	msgID := pubToken.MessageID()

	s.l.Lock()

	publishDate, ok := s.publishDates[msgID]
	if !ok {
		return
	}

	delete(s.publishDates, msgID)
	s.l.Unlock()

	ackWait := now.Sub(publishDate)
	bucket := now.Truncate(bucketSize)

	stats, bucketExists := s.buckets[bucket]
	if !bucketExists {
		s.cleanOldBuckets(now)
	}

	s.l.Lock()
	s.buckets[bucket] = s.updateStats(stats, ackWait)
	s.globalStats = s.updateStats(s.globalStats, ackWait)
	s.l.Unlock()
}

// updateStats returns statistics updated with the given ack duration.
func (s *mqttStats) updateStats(stats bucketStats, ackWait time.Duration) bucketStats {
	min := stats.min
	if ackWait < min || min == 0 {
		min = ackWait
	}

	max := stats.max
	if ackWait > max {
		max = ackWait
	}

	return bucketStats{
		min:        min,
		max:        max,
		avg:        (stats.avg*time.Duration(stats.nbMessages) + ackWait) / time.Duration(stats.nbMessages+1),
		nbMessages: stats.nbMessages + 1,
	}
}

// ackFailed signals that a message failed to be published.
func (s *mqttStats) ackFailed(token paho.Token) {
	pubToken, ok := token.(*paho.PublishToken)
	if !ok {
		return
	}

	s.l.Lock()
	delete(s.publishDates, pubToken.MessageID())
	s.l.Unlock()
}

// cleanOldBuckets remove buckets older than bucketMaxAge.
func (s *mqttStats) cleanOldBuckets(now time.Time) {
	s.l.Lock()
	defer s.l.Unlock()

	for bucket := range s.buckets {
		if bucket.Before(now.Add(-bucketMaxAge)) {
			delete(s.buckets, bucket)
		}
	}
}

// String returns a table containing the statistics.
func (s *mqttStats) String() string {
	s.l.Lock()
	buckets := s.buckets
	global := s.globalStats
	s.l.Unlock()

	var builder strings.Builder

	_, _ = builder.WriteString(
		fmt.Sprintf(
			"Global stats (min/avg/max): %v/%v/%v on %d messages\n\n",
			global.min, global.avg, global.max, global.nbMessages,
		),
	)

	_, _ = builder.WriteString("start, min, avg, max, messages\n")

	for bucket, stats := range buckets {
		_, _ = builder.WriteString(
			fmt.Sprintf("%v, %v, %v, %v, %v\n", bucket, stats.min, stats.avg, stats.max, stats.nbMessages),
		)
	}

	return builder.String()
}
