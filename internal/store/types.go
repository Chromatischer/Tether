package store

type User struct {
	ID       int64
	Username string
	Role     string
}

type Conversation struct {
	ID     int64
	UserID int64
	Title  string
}

type Message struct {
	ID             int64
	ConversationID int64
	Role           string
	Content        string
	IsNotice       bool
	CreatedAt      string
}
