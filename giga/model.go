package giga

type MessageAnalysis struct {
	IsSpam         bool   `json:"is_spam"`
	SpamReason     string `json:"spam_reason"`
	IsItRelated    bool   `json:"is_it_related"`
	IsToxic        bool   `json:"is_toxic"`
	ToxicReason    string `json:"toxic_reason"`
	IsOffTopic     bool   `json:"is_offtopic"`
	OffTopicReason string `json:"offtopic_reason"`
}
