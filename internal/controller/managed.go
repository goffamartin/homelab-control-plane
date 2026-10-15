package controller

import (
	"hlcp/internal/localapi"
	"hlcp/internal/managed"
	"io"
	"net/http"
)

func (server *Server) registerManagedAPI(mux *http.ServeMux) {
	registry := server.config.ManagedRegistry
	if registry == nil {
		return
	}
	mux.HandleFunc("GET /v1/certificates", func(w http.ResponseWriter, r *http.Request) {
		if !localapi.GlobalRead(r.Context()) {
			forbidden(w)
			return
		}
		if server.config.CertificateStatus == nil {
			localapi.Error(w, 503, "CERTIFICATES_UNAVAILABLE", "certificate diagnostics unavailable")
			return
		}
		writeJSON(w, server.config.CertificateStatus())
	})
	mux.HandleFunc("GET /v1/pairing", func(w http.ResponseWriter, r *http.Request) {
		if !localapi.Allowed(r.Context(), "", "", true) || !localapi.GlobalRead(r.Context()) {
			forbidden(w)
			return
		}
		info, err := registry.List()
		if err != nil {
			localapi.Error(w, 503, "REGISTRY_UNAVAILABLE", "registry unavailable")
			return
		}
		writeJSON(w, info)
	})
	mux.HandleFunc("POST /v1/pairing", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		if err != nil {
			localapi.Error(w, 400, "INVALID_REQUEST", "invalid request")
			return
		}
		var request struct {
			Node    string `json:"node"`
			Replace bool   `json:"replace"`
		}
		if managed.DecodeJSON(data, &request, 4096) != nil {
			localapi.Error(w, 400, "INVALID_REQUEST", "invalid request")
			return
		}
		if !localapi.Allowed(r.Context(), request.Node, "", true) {
			forbidden(w)
			return
		}
		var invitation managed.Invitation
		err = localapi.GuardAdmission(r.Context(), request.Node, "", func() error {
			var e error
			invitation, e = registry.CreateInvitation(r.Context(), request.Node, server.config.EnrollmentURL, request.Replace)
			return e
		})
		if err != nil {
			localapi.Error(w, 409, "PAIRING_REFUSED", "pairing refused; inspect existing admission")
			return
		}
		if request.Replace {
			server.sessions.Disconnect(request.Node)
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, struct {
			Code string `json:"pairing_code"`
		}{invitation.Encode()})
	})
	mux.HandleFunc("DELETE /v1/pairing/{node}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("node")
		if !localapi.Allowed(r.Context(), id, "", true) {
			forbidden(w)
			return
		}
		if localapi.GuardAdmission(r.Context(), id, "", func() error { return registry.Revoke(r.Context(), id) }) != nil {
			localapi.Error(w, 409, "REVOCATION_REFUSED", "revocation refused")
			return
		}
		server.sessions.Disconnect(id)
		writeJSON(w, struct {
			Revoked bool `json:"revoked"`
		}{true})
	})
}
