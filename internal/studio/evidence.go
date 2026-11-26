package studio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"

	"github.com/xiejun23/Knooo/internal/distill"
	"github.com/xiejun23/Knooo/internal/sessions"
)

func messageEvidenceVersion(message sessions.Message) string {
	content, _ := json.Marshal(message)
	version := sha256.Sum256(content)
	return hex.EncodeToString(version[:])
}

func stampTopicEvidence(topics []distill.Topic, messages []sessions.Message) []distill.Topic {
	if len(topics) == 0 {
		return topics
	}
	wanted := make(map[string]bool)
	for _, topic := range topics {
		for _, id := range topic.Evidence {
			wanted[id] = true
		}
	}
	versions := make(map[string]string, len(wanted))
	for _, message := range messages {
		if wanted[message.ID] {
			if _, exists := versions[message.ID]; exists {
				versions[message.ID] = ""
			} else {
				versions[message.ID] = messageEvidenceVersion(message)
			}
		}
	}
	result := slices.Clone(topics)
	for i := range result {
		topic := &result[i]
		topic.EvidenceVersions = make(map[string]string, len(topic.Evidence))
		for _, id := range topic.Evidence {
			if version := versions[id]; version != "" {
				topic.EvidenceVersions[id] = version
			}
		}
	}
	return result
}

func checkTopicEvidence(topics []distill.Topic, messages []sessions.Message) []distill.Topic {
	current := stampTopicEvidence(topics, messages)
	for i, topic := range topics {
		stamped := current[i]
		current[i] = topic
		current[i].EvidenceStale = len(topic.Evidence) > 0 && !sameTopicEvidence(topic, stamped)
	}
	return current
}

func sameTopicEvidence(topic, current distill.Topic) bool {
	return len(current.EvidenceVersions) == len(topic.Evidence) && maps.Equal(topic.EvidenceVersions, current.EvidenceVersions)
}
