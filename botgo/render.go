package main

func (a *App) sendContent(chatID int64, key string, vars map[string]string, markup *InlineKeyboardMarkup) (*Message, error) {
	text := a.content.Message(key, vars)
	if photo := a.content.Photo(key); photo != "" {
		if len([]rune(text)) > 1024 {
			if _, err := a.tg.SendPhoto(chatID, photo, "", nil); err != nil {
				return nil, err
			}
			return a.tg.SendMessage(chatID, text, markup)
		}
		return a.tg.SendPhoto(chatID, photo, text, markup)
	}
	return a.tg.SendMessage(chatID, text, markup)
}
