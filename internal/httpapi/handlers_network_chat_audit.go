package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"secure-brain/internal/application"
	"secure-brain/internal/domain"
	openaiapi "secure-brain/internal/openai"
)

func (a *API) getNetwork(w http.ResponseWriter, r *http.Request) {
	userID := activeUser(r.Context()).ID
	nodes, err := a.store.SearchNodes(r.Context(), "", 200)
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	nodeOutput := make([]networkNodeDTO, 0, len(nodes))
	nodeSet := map[string]bool{}
	for _, node := range nodes {
		nodeSet[node.ID] = true
		ports := []string{"data"}
		if node.Type == "service" {
			ports = []string{"input", "output"}
		}
		nodeOutput = append(nodeOutput, networkNodeDTO{
			ID: node.ID, Type: node.Type, DisplayName: node.DisplayName,
			Owned: node.OwnerUserID == userID, Status: node.Status, Ports: ports,
			OnCanvas: a.networkCanvas.onCanvas(userID, node.Type, node.ID),
		})
	}
	routeRows, err := a.store.ListNetworkRoutes(r.Context())
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	edges := []networkEdgeDTO{}
	routes := []networkRouteDTO{}
	for _, route := range routeRows {
		visible := route.Visibility == domain.VisibilityPublic || route.SourceOwnerUserID == userID || contains(route.AllowedBrainOwnerUserIDs, userID) || (route.DestinationOwnerUserID != nil && *route.DestinationOwnerUserID == userID)
		if !visible {
			continue
		}
		serviceHops := make([]string, 0, len(route.Hops))
		for _, hop := range route.Hops {
			serviceHops = append(serviceHops, hop.CanonicalID)
		}
		terminal := "caller"
		if route.DestinationCanonicalID != nil {
			terminal = *route.DestinationCanonicalID
		}
		routes = append(routes, networkRouteDTO{
			ID: route.RouteID, QueryPathID: route.QueryPathID,
			SourceBrainID: route.SourceCanonicalID, Path: route.Path,
			Operations: route.Operations, Visibility: route.Visibility,
			ServiceHops: serviceHops, Terminal: terminal,
			Sequence: append(append([]string{route.SourceCanonicalID}, serviceHops...), terminal),
			Label:    route.SourceCanonicalID + ":" + route.Path,
		})
		from := route.SourceCanonicalID
		for index, hop := range route.Hops {
			edges = append(edges, networkEdgeDTO{ID: route.RouteID + ":" + itoa(index), RouteID: route.RouteID, QueryPathID: route.QueryPathID, HopIndex: index, From: from, To: hop.CanonicalID, Kind: "service_route", Label: route.SourceCanonicalID + ":" + route.Path})
			from = hop.CanonicalID
		}
		terminalNode := "caller:" + route.RouteID
		if route.DestinationCanonicalID != nil {
			terminalNode = *route.DestinationCanonicalID
		} else if !nodeSet[terminalNode] {
			nodeSet[terminalNode] = true
			nodeOutput = append(nodeOutput, networkNodeDTO{ID: terminalNode, Type: "terminal", DisplayName: "Caller", Owned: false, Status: domain.NodeStatusReady, Ports: []string{"destination"}, OnCanvas: true})
		}
		hopIndex := len(route.Hops)
		edges = append(edges, networkEdgeDTO{ID: route.RouteID + ":" + itoa(hopIndex), RouteID: route.RouteID, QueryPathID: route.QueryPathID, HopIndex: hopIndex, From: from, To: terminalNode, Kind: "service_route", Label: route.SourceCanonicalID + ":" + route.Path})
	}
	writeData(w, r, http.StatusOK, networkDTO{Nodes: nodeOutput, Edges: edges, Routes: routes})
}

func (a *API) searchNetwork(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(query)) > 120 {
		writeError(w, r, domain.NewError(domain.CodeInvalidRequest, "The search query is too long."))
		return
	}
	nodes, err := a.store.SearchNodes(r.Context(), query, 50)
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	userID := activeUser(r.Context()).ID
	result := make([]networkNodeDTO, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, networkNodeDTO{
			ID: node.ID, Type: node.Type, DisplayName: node.DisplayName,
			Owned: node.OwnerUserID == userID, Status: node.Status,
			OnCanvas: a.networkCanvas.onCanvas(userID, node.Type, node.ID),
		})
	}
	writeData(w, r, http.StatusOK, result)
}

func (a *API) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	status := domain.AuditStatus(r.URL.Query().Get("status"))
	if status != "" && status != domain.AuditStatusAllowed && status != domain.AuditStatusDenied && status != domain.AuditStatusSucceeded && status != domain.AuditStatusFailed && status != domain.AuditStatusPending {
		writeError(w, r, domain.NewError(domain.CodeInvalidRequest, "The audit status is invalid."))
		return
	}
	events, err := a.store.ListAuditEvents(r.Context(), activeUser(r.Context()).ID, application.AuditQuery{NodeID: r.URL.Query().Get("node_id"), EventType: r.URL.Query().Get("event_type"), Status: status, Limit: 50})
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	writeData(w, r, http.StatusOK, auditEventListResponse(events))
}

func (a *API) getChat(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	messages, err := a.store.ListChatMessages(r.Context(), brain.ID, activeUser(r.Context()).ID, a.chatHistoryMessages)
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	writeData(w, r, http.StatusOK, chatResponse(messages))
}

func (a *API) postChat(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if a.chatDisabled || a.chat == nil {
		writeError(w, r, domain.NewError(domain.CodeChatProviderError, "Chat is disabled for this server."))
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	body.Message = strings.TrimSpace(body.Message)
	count := len([]rune(body.Message))
	if count == 0 || count > 4000 {
		writeError(w, r, domain.NewError(domain.CodeInvalidRequest, "Chat messages must contain 1 to 4,000 characters."))
		return
	}
	history, err := a.store.ListChatMessages(r.Context(), brain.ID, activeUser(r.Context()).ID, a.chatHistoryMessages)
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	input := make([]openaiapi.Message, 0, len(history)+1)
	for _, message := range history {
		input = append(input, openaiapi.Message{Role: string(message.Role), Content: message.Content})
	}
	input = append(input, openaiapi.Message{Role: "user", Content: body.Message})
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	response, err := a.chat.Respond(ctx, openaiapi.Request{Model: a.chatModel, Instructions: openaiapi.BrainInstructions(brain.DisplayName, brain.CanonicalID), Input: input, MaxOutputTokens: a.chatMaxOutputTokens})
	if err != nil {
		a.audit(r, "chat.failed", "brain", brain.ID, &brain.ID, nil, nil, domain.AuditStatusFailed, application.AuditMetadata{"error_code": domain.CodeChatProviderError}, []string{activeUser(r.Context()).ID})
		writeError(w, r, err)
		return
	}
	messages, err := a.store.InsertChatPair(r.Context(), brain.ID, activeUser(r.Context()).ID, body.Message, response, a.chatModel)
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	a.audit(r, "chat.requested", "brain", brain.ID, &brain.ID, nil, nil, domain.AuditStatusSucceeded, application.AuditMetadata{"model": a.chatModel}, []string{activeUser(r.Context()).ID})
	writeData(w, r, http.StatusOK, chatResponse(messages))
}

func (a *API) clearChat(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if _, err := a.store.ClearChat(r.Context(), brain.ID, activeUser(r.Context()).ID); err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	result := ""
	for value > 0 {
		result = string(rune('0'+value%10)) + result
		value /= 10
	}
	return result
}
