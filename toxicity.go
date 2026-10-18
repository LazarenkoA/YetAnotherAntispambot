package app

import (
	"encoding/json"
	"fmt"
	"math"
)

const (
	toxicityStatsKey = "toxicityStats" // ключ redis, в котором хранится статистика токсичности
)

// ToxicityStat - статистика токсичности сообщений пользователя в чате.
// Учитываются только сообщения, которые реально прошли анализ через ИИ:
// если API недоступен (нет денег на счету, ошибка сети и т.п.), сообщение не попадает в статистику,
// что бы % токсичности не искажался в сторону уменьшения
type ToxicityStat struct {
	Total int `json:"total"` // всего проверенных ИИ сообщений
	Toxic int `json:"toxic"` // из них токсичных (hate_percent >= hateToxicThreshold)
}

// Percent возвращает % токсичных сообщений (0-100), округленный до целого
func (s ToxicityStat) Percent() float64 {
	if s.Total <= 0 {
		return 0
	}

	return math.Round(float64(s.Toxic) / float64(s.Total) * 100)
}

func (s ToxicityStat) String() string {
	return fmt.Sprintf("toxic: %d, total: %d, percent: %.0f%%", s.Toxic, s.Total, s.Percent())
}

// RecordToxicity фиксирует результат анализа сообщения пользователя. Возвращает обновленную статистику
func (wd *Telega) RecordToxicity(chatID, userID int64, hatePercent int) ToxicityStat {
	wd.mx.Lock()
	defer wd.mx.Unlock()

	if wd.toxicity == nil {
		wd.toxicity = map[int64]map[int64]*ToxicityStat{}
	}

	if _, ok := wd.toxicity[chatID]; !ok {
		wd.toxicity[chatID] = map[int64]*ToxicityStat{}
	}

	if _, ok := wd.toxicity[chatID][userID]; !ok {
		wd.toxicity[chatID][userID] = new(ToxicityStat)
	}

	stat := wd.toxicity[chatID][userID]
	stat.Total++
	if hatePercent >= hateToxicThreshold {
		stat.Toxic++
	}

	return *stat
}

// GetToxicity возвращает статистику токсичности пользователя в чате (нулевую, если еще не считалась)
func (wd *Telega) GetToxicity(chatID, userID int64) ToxicityStat {
	wd.mx.RLock()
	defer wd.mx.RUnlock()

	if users, ok := wd.toxicity[chatID]; ok {
		if stat, ok := users[userID]; ok && stat != nil {
			return *stat
		}
	}

	return ToxicityStat{}
}

func (wd *Telega) restoreToxicityInfo() {
	wd.mx.Lock()
	defer wd.mx.Unlock()

	if wd.toxicity == nil {
		wd.toxicity = map[int64]map[int64]*ToxicityStat{}
	}

	data, err := wd.r.Get(toxicityStatsKey)
	if err != nil {
		// ключа еще нет - это не ошибка
		wd.logger.Info("restoreToxicityInfo: статистика токсичности не найдена, начинаем с нуля")
		return
	}

	if err := json.Unmarshal([]byte(data), &wd.toxicity); err != nil {
		wd.logger.Error(fmt.Errorf("restoreToxicityInfo error: %w", err).Error())
	}
}

func (wd *Telega) storeToxicityInfo() {
	wd.mx.RLock()
	defer wd.mx.RUnlock()

	data, err := json.Marshal(wd.toxicity)
	if err != nil {
		wd.logger.Error(fmt.Errorf("storeToxicityInfo error: %w", err).Error())
		return
	}

	if err := wd.r.Set(toxicityStatsKey, string(data), -1); err != nil {
		wd.logger.Error(fmt.Errorf("storeToxicityInfo error: %w", err).Error())
	}
}
