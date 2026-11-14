package ui

import (
	"gioui.org/widget"
	mdicons "golang.org/x/exp/shiny/materialdesign/icons"
)

// icons is the fixed icon set used by the UI. All glyphs are Material Design
// icons compiled into the binary; nothing is fetched at runtime.
type icons struct {
	Send       *widget.Icon
	Add        *widget.Icon
	Save       *widget.Icon
	Delete     *widget.Icon
	Settings   *widget.Icon
	Search     *widget.Icon
	History    *widget.Icon
	Lock       *widget.Icon
	Unlock     *widget.Icon
	Sync       *widget.Icon
	Cloud      *widget.Icon
	CloudOff   *widget.Icon
	Check      *widget.Icon
	Close      *widget.Icon
	Chevron    *widget.Icon
	Expand     *widget.Icon
	Dropdown   *widget.Icon
	More       *widget.Icon
	Folder     *widget.Icon
	Request    *widget.Icon
	Code       *widget.Icon
	Download   *widget.Icon
	Upload     *widget.Icon
	Error      *widget.Icon
	Warning    *widget.Icon
	Info       *widget.Icon
	Cancel     *widget.Icon
	Email      *widget.Icon
	ArrowUp    *widget.Icon
	ArrowDown  *widget.Icon
	ArrowBack  *widget.Icon
	Refresh    *widget.Icon
	CloudUp    *widget.Icon
	CloudDown  *widget.Icon
	Person     *widget.Icon
	Home       *widget.Icon
	Play       *widget.Icon
	Stop       *widget.Icon
	Terminal   *widget.Icon
	Link       *widget.Icon
	Copy       *widget.Icon
	Edit       *widget.Icon
	FileExport *widget.Icon
	FileImport *widget.Icon
}

func loadIcons() icons {
	return icons{
		Send:       mustIcon(mdicons.ContentSend),
		Add:        mustIcon(mdicons.ContentAdd),
		Save:       mustIcon(mdicons.ContentSave),
		Delete:     mustIcon(mdicons.ActionDelete),
		Settings:   mustIcon(mdicons.ActionSettings),
		Search:     mustIcon(mdicons.ActionSearch),
		History:    mustIcon(mdicons.ActionHistory),
		Lock:       mustIcon(mdicons.ActionLock),
		Unlock:     mustIcon(mdicons.ActionLockOpen),
		Sync:       mustIcon(mdicons.NotificationSync),
		Cloud:      mustIcon(mdicons.FileCloud),
		CloudOff:   mustIcon(mdicons.FileCloudOff),
		Check:      mustIcon(mdicons.ActionDone),
		Close:      mustIcon(mdicons.NavigationClose),
		Chevron:    mustIcon(mdicons.NavigationChevronRight),
		Expand:     mustIcon(mdicons.NavigationExpandMore),
		Dropdown:   mustIcon(mdicons.NavigationArrowDropDown),
		More:       mustIcon(mdicons.NavigationMoreVert),
		Folder:     mustIcon(mdicons.FileFolder),
		Request:    mustIcon(mdicons.ActionDescription),
		Code:       mustIcon(mdicons.ActionCode),
		Download:   mustIcon(mdicons.FileCloudDownload),
		Upload:     mustIcon(mdicons.FileCloudUpload),
		Error:      mustIcon(mdicons.AlertError),
		Warning:    mustIcon(mdicons.AlertWarning),
		Info:       mustIcon(mdicons.ActionInfo),
		Cancel:     mustIcon(mdicons.NavigationCancel),
		Email:      mustIcon(mdicons.CommunicationEmail),
		ArrowUp:    mustIcon(mdicons.NavigationArrowUpward),
		ArrowDown:  mustIcon(mdicons.NavigationArrowDownward),
		ArrowBack:  mustIcon(mdicons.NavigationArrowBack),
		Refresh:    mustIcon(mdicons.NavigationRefresh),
		CloudUp:    mustIcon(mdicons.FileCloudUpload),
		CloudDown:  mustIcon(mdicons.FileCloudDownload),
		Person:     mustIcon(mdicons.SocialPerson),
		Home:       mustIcon(mdicons.ActionHome),
		Play:       mustIcon(mdicons.AVPlayArrow),
		Stop:       mustIcon(mdicons.AVStop),
		Terminal:   mustIcon(mdicons.ActionCode),
		Link:       mustIcon(mdicons.ContentLink),
		Copy:       mustIcon(mdicons.ContentContentCopy),
		Edit:       mustIcon(mdicons.EditorModeEdit),
		FileExport: mustIcon(mdicons.FileCloudDownload),
		FileImport: mustIcon(mdicons.FileCloudUpload),
	}
}

// mustIcon panics on corrupt icon data. The icons are embedded constants, so
// a failure here is a build problem rather than a runtime condition.
func mustIcon(data []byte) *widget.Icon {
	ic, err := widget.NewIcon(data)
	if err != nil {
		panic("yourpm: invalid embedded icon: " + err.Error())
	}
	return ic
}
