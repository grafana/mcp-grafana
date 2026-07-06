package tools

// OnCall response types used by MCP tools.
// These types provide a unified interface regardless of whether the data
// comes from the IRM plugin proxy (internal API) or the public OnCall API.

// OnCallAlertGroup represents an alert group returned by MCP tools.
type OnCallAlertGroup struct {
	ID             string            `json:"id"`
	IntegrationID  string            `json:"integration_id,omitempty"`
	AlertsCount    int               `json:"alerts_count"`
	State          string            `json:"state"`
	CreatedAt      string            `json:"created_at"`
	ResolvedAt     string            `json:"resolved_at,omitempty"`
	AcknowledgedAt string            `json:"acknowledged_at,omitempty"`
	SilencedAt     string            `json:"silenced_at,omitempty"`
	Title          string            `json:"title,omitempty"`
	Permalinks     map[string]string `json:"permalinks,omitempty"`
	Labels         any               `json:"labels,omitempty"`
	Team           string            `json:"team,omitempty"`
}

// OnCallUser represents an OnCall user returned by MCP tools.
type OnCallUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
	Role     string `json:"role,omitempty"`
	Name     string `json:"name,omitempty"`
}

// OnCallTeam represents an OnCall team returned by MCP tools.
type OnCallTeam struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

// OnCallShift represents an on-call shift returned by MCP tools.
type OnCallShift struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Type          any      `json:"type"`
	Schedule      string   `json:"schedule,omitempty"`
	PriorityLevel int      `json:"priority_level,omitempty"`
	ShiftStart    string   `json:"shift_start,omitempty"`
	ShiftEnd      any      `json:"shift_end,omitempty"`
	RotationStart string   `json:"rotation_start,omitempty"`
	Until         string   `json:"until,omitempty"`
	Frequency     any      `json:"frequency,omitempty"`
	Interval      int      `json:"interval,omitempty"`
	ByDay         []string `json:"by_day,omitempty"`
	WeekStart     string   `json:"week_start,omitempty"`
	RollingUsers  any      `json:"rolling_users,omitempty"`
}

// onCallScheduleInternal is the internal API response for a schedule.
// Used to parse the IRM proxy response before mapping to ScheduleSummary.
type onCallScheduleInternal struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Type      any      `json:"type"`
	Team      any      `json:"team"`
	TimeZone  string   `json:"time_zone"`
	OnCallNow any      `json:"on_call_now"`
	Shifts    []string `json:"shifts"`
}

// onCallAlertGroupInternal is the internal API response for an alert group.
type onCallAlertGroupInternal struct {
	PK                  string            `json:"pk"`
	AlertsCount         int               `json:"alerts_count"`
	Status              any               `json:"status"`
	StartedAt           string            `json:"started_at"`
	ResolvedAt          string            `json:"resolved_at"`
	AcknowledgedAt      string            `json:"acknowledged_at"`
	SilencedAt          string            `json:"silenced_at"`
	AlertReceiveChannel any               `json:"alert_receive_channel"`
	Team                any               `json:"team"`
	Labels              any               `json:"labels"`
	RenderForWeb        *renderForWeb     `json:"render_for_web"`
	Permalinks          map[string]string `json:"permalinks"`
}

type renderForWeb struct {
	Title   string `json:"title"`
	Message string `json:"message"`
}

// onCallTeamInternal is the internal API response for a team.
type onCallTeamInternal struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

func (t *onCallTeamInternal) toOnCallTeam() *OnCallTeam {
	return &OnCallTeam{
		ID:        t.ID,
		Name:      t.Name,
		Email:     t.Email,
		AvatarURL: t.AvatarURL,
	}
}

// onCallShiftInternal is the internal API response for a shift.
type onCallShiftInternal struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Type          any      `json:"type"`
	Schedule      string   `json:"schedule"`
	PriorityLevel int      `json:"priority_level"`
	ShiftStart    string   `json:"shift_start"`
	ShiftEnd      any      `json:"shift_end"`
	RotationStart string   `json:"rotation_start"`
	Until         string   `json:"until"`
	Frequency     any      `json:"frequency"`
	Interval      int      `json:"interval"`
	ByDay         []string `json:"by_day"`
	WeekStart     string   `json:"week_start"`
	RollingUsers  any      `json:"rolling_users"`
}

func (s *onCallShiftInternal) toOnCallShift() *OnCallShift {
	return &OnCallShift{
		ID:            s.ID,
		Name:          s.Name,
		Type:          s.Type,
		Schedule:      s.Schedule,
		PriorityLevel: s.PriorityLevel,
		ShiftStart:    s.ShiftStart,
		ShiftEnd:      s.ShiftEnd,
		RotationStart: s.RotationStart,
		Until:         s.Until,
		Frequency:     s.Frequency,
		Interval:      s.Interval,
		ByDay:         s.ByDay,
		WeekStart:     s.WeekStart,
		RollingUsers:  s.RollingUsers,
	}
}

// onCallUserInternal is the internal API response for a user.
type onCallUserInternal struct {
	PK       string `json:"pk"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Role     any    `json:"role"`
}

// alertGroupStatusToState converts the internal API numeric status to a string state.
func alertGroupStatusToState(status any) string {
	switch v := status.(type) {
	case float64:
		switch int(v) {
		case 0:
			return "new"
		case 1:
			return "acknowledged"
		case 2:
			return "resolved"
		case 3:
			return "silenced"
		}
	case string:
		return v
	}
	return "unknown"
}

func (ag *onCallAlertGroupInternal) toOnCallAlertGroup() *OnCallAlertGroup {
	result := &OnCallAlertGroup{
		ID:             ag.PK,
		AlertsCount:    ag.AlertsCount,
		State:          alertGroupStatusToState(ag.Status),
		CreatedAt:      ag.StartedAt,
		ResolvedAt:     ag.ResolvedAt,
		AcknowledgedAt: ag.AcknowledgedAt,
		SilencedAt:     ag.SilencedAt,
		Labels:         ag.Labels,
		Permalinks:     ag.Permalinks,
	}
	if ag.RenderForWeb != nil {
		result.Title = ag.RenderForWeb.Title
	}
	if m, ok := ag.AlertReceiveChannel.(map[string]any); ok {
		if id, ok := m["id"].(string); ok {
			result.IntegrationID = id
		}
	}
	if t, ok := ag.Team.(string); ok {
		result.Team = t
	}
	return result
}

func (u *onCallUserInternal) toOnCallUser() *OnCallUser {
	result := &OnCallUser{
		ID:       u.PK,
		Username: u.Username,
		Email:    u.Email,
		Name:     u.Name,
	}
	if r, ok := u.Role.(string); ok {
		result.Role = r
	}
	return result
}
