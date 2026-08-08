package views

import (
	"mumu-bot/internal/logger"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/modelstats"
	"mumu-bot/internal/web/services"
)

type NavItem struct {
	Label string
	Href  string
}

type FlashMessage struct {
	Kind  string
	Title string
	Body  string
}

type RowAction struct {
	Label       string
	Value       string
	Kind        string
	BusyLabel   string
	ConfirmText string
}

type AdminActionChip struct {
	Label string
	Kind  string
}

type AdminActionField struct {
	Label string
	Value string
}

type AdminActionHiddenField struct {
	Name  string
	Value string
}

type AdminActionDialogContentData struct {
	Title       string
	Body        string
	SubmitLabel string
	SubmitClass string
	BusyLabel   string
	Spotlight   string
	Chips       []AdminActionChip
	Fields      []AdminActionField
	Hidden      []AdminActionHiddenField
	ReturnTo    string
}

type StickerPreviewDialogData struct {
	FileURL     string
	Description string
	FileName    string
	FileHash    string
	Meta        string
}

type LayoutData struct {
	Title       string
	CurrentPath string
	NavItems    []NavItem
	Flash       *FlashMessage
}

type SortToolbarLink struct {
	Label  string
	Href   string
	Active bool
}

type SortToolbarData struct {
	Summary      string
	CurrentSort  string
	CurrentOrder string
	Options      []SortToolbarLink
	OrderOptions []SortToolbarLink
}

type FilterChoice struct {
	Label string
	Value string
}

type LoginPageData struct {
	Enabled bool
	Error   string
}

type DashboardPageData struct {
	BotName           string
	EnabledGroupCount int
	MemoryCount       int64
	MemberCount       int64
	JargonCount       int64
	StyleCardCount    int64
	StickerCount      int64
	OneBotConnected   bool
	SelfID            int64
	MCPToolCount      int
	LearningEnabled   bool
	CurrentMood       *memory.MoodState
	Flash             *FlashMessage
}

type ListMeta struct {
	Total    int64
	Page     int
	PageSize int
	PrevURL  string
	NextURL  string
}

type StyleCardListPageData struct {
	GroupID string
	Status  string
	Keyword string
	Sort    SortToolbarData
	Items   []services.StylePatternView
	Meta    ListMeta
	Flash   *FlashMessage
}

type JargonListPageData struct {
	GroupID string
	Status  string
	Keyword string
	Sort    SortToolbarData
	Items   []services.JargonView
	Meta    ListMeta
	Flash   *FlashMessage
}

type StickerListPageData struct {
	Keyword string
	Sort    SortToolbarData
	Items   []memory.Sticker
	Meta    ListMeta
	Flash   *FlashMessage
}

type MemoryListPageData struct {
	GroupID string
	Subject string
	Status  string
	Kind    string
	Keyword string
	Sort    SortToolbarData
	Items   []services.MemoryView
	SelfID  int64
	Meta    ListMeta
	Flash   *FlashMessage
}

type TopicListPageData struct {
	GroupID string
	Status  string
	Keyword string
	Sort    SortToolbarData
	Items   []services.TopicThreadView
	Meta    ListMeta
	Flash   *FlashMessage
}

type TopicDetailPageData struct {
	Thread         services.TopicThreadView
	SummaryChanges []TopicSummaryChangeView
	Messages       []memory.MessageLog
	Flash          *FlashMessage
}

type MemberListPageData struct {
	Keyword string
	Sort    SortToolbarData
	Items   []services.MemberProfileView
	Meta    ListMeta
	Flash   *FlashMessage
}

type SystemField struct {
	Label string
	Value string
}

type SystemSection struct {
	Title  string
	Fields []SystemField
}

type SystemPageData struct {
	View        string
	Sections    []SystemSection
	Logs        []logger.Line
	LogKeyword  string
	LogLevel    string
	LogTotal    int
	LogFiltered int
	StatsRange  string
	Stats       modelstats.Snapshot
	Flash       *FlashMessage
}

type TopicSummaryChangeView struct {
	CapturedAtLabel  string
	CapturedAtValue  string
	Headline         string
	Badges           []TopicSummaryChangeBadgeView
	InitiallyOpen    bool
	InitialSnapshot  bool
	TitleChanged     bool
	CurrentTitle     string
	PreviousTitle    string
	TitleDiff        TopicTextDiffView
	GistChanged      bool
	CurrentGist      string
	PreviousGist     string
	GistDiff         TopicTextDiffView
	AddedClaims      []string
	RemovedClaims    []string
	AddedOpenLoops   []string
	RemovedOpenLoops []string
	Changed          bool
}

type TopicSummaryChangeBadgeView struct {
	Label string
	Tone  string
}

type TopicTextDiffView struct {
	PreviousSegments    []TopicTextDiffSegmentView
	CurrentSegments     []TopicTextDiffSegmentView
	InlineSegments      []TopicTextDiffSegmentView
	PreviousPlaceholder string
	CurrentPlaceholder  string
	PreviousEmpty       bool
	CurrentEmpty        bool
}

type TopicTextDiffSegmentView struct {
	Text string
	Kind string
}
