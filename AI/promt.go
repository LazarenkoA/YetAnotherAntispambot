package AI

import "fmt"

func PromptGetSpamPercent(msg string) string {
	return fmt.Sprintf(`По одному сообщению ты должен определить его характеристики и обосновать выводы.
								
								Определи и выведи результат строго в формате JSON:
								
								- is_spam: Является ли сообщение спамом (true/false)
								- spam_reason: Краткое объяснение, почему сообщение определено (или не определено) как спам
								- hate_percent: Оценка злобы, агрессии в сообщении от 0 до 100, где 0 это обычное сообщение, 100 явно агрессивное сообщение
								- hate_reason: Краткое объяснение, если сообщение признано токсичным или агрессивным
								- is_offtopic: Является ли сообщение оффтопом (true/false)
								- offtopic_reason: Краткое объяснение, почему сообщение сочтено оффтопом (или нет)
								

								ПРИМЕР того как должен выглядить ответ:
								
								{
								  "is_spam": true,
								  "spam_reason": "Сообщение содержит рекламу сомнительного заработка и внешнюю ссылку.",
								  "hate_percent": 0,
								  "hate_reason": "Нет признаков агрессии или оскорблений.",
								  "is_offtopic": true,
								  "offtopic_reason": "Тематика сообщения не связана с IT и не относится к текущему обсуждению."
								}						

								ПЕРЕД ОТВЕТОМ ПРОВЕРЬ JSON НА ВАЛИДНОСТЬ

								Вот сообщение для анализа:
								"%s"`, msg)
}
