package session

// sendReqDone 业务成功后的完整回执（需已设置 currentReqID）。
func (u *User) sendReqDone(detail string) {
	if u.currentReqID == "" {
		return
	}
	line := "REQ_DONE|" + u.currentReqID
	if detail != "" {
		line += "|" + detail
	}
	u.Send(PackTextMessageWithReqID(line, u.currentReqID))
}

func (u *User) sendTextAndReqDone(text, detail string) {
	u.Send(PackTextMessage(text))
	u.sendReqDone(detail)
}
