package giga

type MessageAnalysis struct {
	IsSpam         bool   `json:"is_spam"`
	SpamReason     string `json:"spam_reason"`
	IsItRelated    bool   `json:"is_it_related"`
	HatePercent    int    `json:"hate_percent"`
	HateReason     string `json:"hate_reason"`
	IsOffTopic     bool   `json:"is_offtopic"`
	OffTopicReason string `json:"offtopic_reason"`
}
