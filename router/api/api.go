package api

import (
	"net/http"

	"gopkg.in/macaron.v1"
	"github.com/go-macaron/binding"

	"github.com/emersion/neutron/backend"
)

type RespCode int

const (
	Ok RespCode = 1000
	Batch = 1001

	BadRequest = 400
	Unauthorized = 401
	NotFound = 404

	InternalServerError = 500
)

type Req struct {}

type Resp struct {
	Code RespCode
}

type ErrorResp struct {
	Resp
	Error string
	ErrorDescription string
}

func newErrorResp(err error) *ErrorResp {
	return &ErrorResp{
		Resp: Resp{InternalServerError},
		Error: "unknown_error",
		ErrorDescription: err.Error(),
	}
}

type BatchReq struct {
	Req
	IDs []string
}

type BatchResp struct {
	Resp
	Responses []*BatchRespItem
}

func newBatchResp(items []*BatchRespItem) *BatchResp {
	return &BatchResp{
		Resp: Resp{Batch},
		Responses: items,
	}
}

type BatchRespItem struct {
	ID string
	Response interface{}
}

type Api struct {
	backend *backend.Backend
	sessions map[string]*Session
}

func (api *Api) getUid(ctx *macaron.Context) string {
	uid, ok := ctx.Data["uid"]
	if !ok {
		return ""
	}

	return uid.(string)
}

func (api *Api) getSessionToken(ctx *macaron.Context) string {
	sessionToken, ok := ctx.Data["sessionToken"]
	if !ok {
		return ""
	}

	return sessionToken.(string)
}

func (api *Api) getSession(ctx *macaron.Context) (session *Session) {
	sessionToken := api.getSessionToken(ctx)
	if sessionToken == "" {
		return
	}

	for _, s := range api.sessions {
		if s.Token == sessionToken {
			session = s
			return
		}
	}

	return
}

func (api *Api) getUserId(ctx *macaron.Context) string {
	session := api.getSession(ctx)
	if session == nil {
		return ""
	}

	return session.UserID
}

func (api *Api) keepSessionAlive(ctx *macaron.Context) {
	session := api.getSession(ctx)
	if session == nil {
		return
	}

	session.Timeout.Reset(SessionTimeout)
}

func New(m *macaron.Macaron, backend *backend.Backend) {
	api := &Api{
		backend: backend,
		sessions: map[string]*Session{},
	}

	m.Use(func (ctx *macaron.Context) {
		if appVersion, ok := ctx.Req.Header["X-Pm-Appversion"]; ok {
			ctx.Data["appVersion"] = appVersion[0]
		}
		if apiVersion, ok := ctx.Req.Header["X-Pm-Apiversion"]; ok {
			ctx.Data["apiVersion"] = apiVersion[0]
		}
		if sessionToken, ok := ctx.Req.Header["X-Pm-Session"]; ok {
			ctx.Data["sessionToken"] = sessionToken[0]
		}
		if uid, ok := ctx.Req.Header["X-Pm-Uid"]; ok {
			ctx.Data["uid"] = uid[0]
		}

		api.keepSessionAlive(ctx)
	})

	m.Group("/attachments", func() {
		m.Get("/:id", api.GetAttachment)
		m.Post("/upload", binding.MultipartForm(UploadAttachmentReq{}), api.UploadAttachment)
		m.Put("/remove", binding.Json(RemoveAttachmentReq{}), api.RemoveAttachment)
	})

	m.Group("/auth", func() {
		m.Post("/", binding.Json(AuthReq{}), api.Auth)
		m.Delete("/", api.DeleteAuth)
		m.Post("/cookies", binding.Json(AuthCookiesReq{}), api.AuthCookies)
	})

	m.Group("/users", func() {
		m.Get("/", api.GetCurrentUser)
		m.Post("/", binding.Json(CreateUserReq{}), api.CreateUser)
		m.Get("/direct", api.GetDirectUser)
		m.Get("/available/:username", api.GetUsernameAvailable)
		m.Get("/pubkeys/:email", api.GetPublicKey)
	})

	m.Group("/contacts", func() {
		m.Get("/", api.GetContacts)
		m.Post("/", binding.Json(CreateContactsReq{}), api.CreateContacts)
		m.Delete("/", api.DeleteAllContacts)
		m.Put("/:id", binding.Json(UpdateContactReq{}), api.UpdateContact)
		m.Put("/delete", binding.Json(BatchReq{}), api.DeleteContacts)
	})

	m.Group("/labels", func() {
		m.Get("/", api.GetLabels)
		m.Post("/", binding.Json(LabelReq{}), api.CreateLabel)
		m.Put("/:id", binding.Json(LabelReq{}), api.UpdateLabel)
		m.Put("/order", binding.Json(LabelsOrderReq{}), api.UpdateLabelsOrder)
		m.Delete("/:id", api.DeleteLabel)
	})

	m.Group("/messages", func() {
		m.Get("/", api.ListMessages)
		m.Get("/count", api.GetMessagesCount)
		m.Get("/total", api.GetMessagesTotal)
		m.Get("/:id", api.GetMessage)
		m.Put("/:action(read|unread)", binding.Json(BatchReq{}), api.UpdateMessagesRead)
		m.Put("/:action(star|unstar)", binding.Json(BatchReq{}), api.UpdateMessagesStar)
		m.Put("/:label(trash|inbox|spam|archive)", binding.Json(BatchReq{}), api.UpdateMessagesSystemLabel)
		m.Delete("/:label(draft|spam|trash)", api.DeleteAllMessages)
		m.Post("/draft", binding.Json(MessageReq{}), api.CreateDraft)
		m.Put("/draft/:id", binding.Json(MessageReq{}), api.UpdateDraft)
		m.Post("/send/:id", binding.Json(SendMessageReq{}), api.SendMessage)
		m.Put("/delete", binding.Json(BatchReq{}), api.DeleteMessages)
		m.Put("/label", binding.Json(UpdateMessagesLabelReq{}), api.UpdateMessagesLabel)
	})

	m.Group("/conversations", func() {
		m.Get("/", api.ListConversations)
		m.Get("/:id", api.GetConversation)
		m.Get("/count", api.GetConversationsCount)
		m.Put("/:action(read|unread)", binding.Json(BatchReq{}), api.UpdateConversationsRead)
		m.Put("/:action(star|unstar)", binding.Json(BatchReq{}), api.UpdateConversationsStar)
		m.Put("/:label(trash|inbox|spam|archive)", binding.Json(BatchReq{}), api.UpdateConversationsSystemLabel)
		m.Put("/delete", binding.Json(BatchReq{}), api.DeleteConversations)
		m.Put("/label", binding.Json(UpdateConversationsLabelReq{}), api.UpdateConversationsLabel)
	})

	m.Group("/events", func() {
		m.Get("/:event", api.GetEvent)
	})

	m.Group("/settings", func() {
		m.Put("/password", binding.Json(UpdateUserPasswordReq{}), api.UpdateUserPassword)
		m.Put("/display", binding.Json(UpdateUserSettingsReq{}), api.UpdateUserDisplayName)
		m.Put("/signature", binding.Json(UpdateUserSettingsReq{}), api.UpdateUserSignature)
		m.Put("/autosave", binding.Json(UpdateUserSettingsReq{}), api.UpdateUserAutoSaveContacts)
		m.Put("/showimages", binding.Json(UpdateUserSettingsReq{}), api.UpdateUserShowImages)

		m.Put("/composermode", binding.Json(UpdateUserSettingsReq{}), api.UpdateUserComposerMode)
		m.Put("/viewlayout", binding.Json(UpdateUserSettingsReq{}), api.UpdateUserViewLayout)
		m.Put("/messagebuttons", binding.Json(UpdateUserSettingsReq{}), api.UpdateUserMessageButtons)
		m.Put("/theme", binding.Json(UpdateUserSettingsReq{}), api.UpdateUserTheme)
	})

	m.Group("/keys", func() {
		m.Post("/", binding.Json(CreatePrivateKeyReq{}), api.CreatePrivateKey)
		m.Put("/private", binding.Json(UpdateAllPrivateKeysReq{}), api.UpdateAllPrivateKeys)
	})

	m.Group("/domains", func() {
		m.Get("/", api.GetUserDomains)
		m.Get("/:id", api.GetDomain)
		m.Get("/available", api.GetAvailableDomains)
	})

	m.Group("/addresses", func() {
		m.Post("/", binding.Json(CreateAddressReq{}), api.CreateAddress)
		m.Put("/:id/:action(enable|disable)", api.ToggleAddress)
		m.Delete("/:id", api.DeleteAddress)
	})

	m.Group("/payments", func() {
		m.Get("/plans", api.GetPlans)
		m.Get("/subscription", api.GetSubscription)
		m.Get("/methods", api.GetPaymentMethods)
		m.Get("/invoices", api.GetInvoices)
	})

	m.Get("/organizations", api.GetUserOrganization)
	m.Get("/members", api.GetMembers)

	m.Post("/bugs/crash", binding.Json(CrashReq{}), api.Crash)

	// Not found
	m.Any("/*", func (ctx *macaron.Context) {
		ctx.JSON(http.StatusNotFound, &ErrorResp{
			Resp: Resp{NotFound},
			Error: "invalid_endpoint",
			ErrorDescription: "Endpoint not found",
		})
	})
}
