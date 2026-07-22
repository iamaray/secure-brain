package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"secure-brain/internal/assets"
	"secure-brain/internal/domain"
	"secure-brain/internal/store"
)

func (a *API) listTransfers(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	_, _ = a.store.ExpirePendingTransfers(r.Context(), a.now())
	direction := r.URL.Query().Get("direction")
	if direction == "" {
		direction = "incoming"
	}
	if direction != "incoming" && direction != "outgoing" {
		writeError(w, r, domain.NewError(domain.CodeInvalidRequest, "direction must be incoming or outgoing."))
		return
	}
	status := domain.TransferStatus(r.URL.Query().Get("status"))
	if status != "" && status != domain.TransferStatusPending && status != domain.TransferStatusAccepted && status != domain.TransferStatusRejected && status != domain.TransferStatusExpired {
		writeError(w, r, domain.NewError(domain.CodeInvalidRequest, "status is invalid."))
		return
	}
	items, err := a.store.ListTransfers(r.Context(), store.TransferFilter{BrainID: brain.ID, Direction: direction, Status: status, Limit: 50})
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	writeData(w, r, http.StatusOK, items)
}

func (a *API) getTransfer(w http.ResponseWriter, r *http.Request) {
	_, _ = a.store.ExpirePendingTransfers(r.Context(), a.now())
	transfer, destinationOwned, err := a.visibleTransfer(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	result := map[string]any{"transfer": transfer}
	if r.URL.Query().Get("preview") == "true" {
		if !destinationOwned {
			writeError(w, r, domain.NewError(domain.CodeNotAuthorized, "Only the destination owner may preview this transfer."))
			return
		}
		body, _, err := a.objects.Get(r.Context(), transfer.StoragePath)
		if err != nil {
			writeError(w, r, err)
			return
		}
		defer body.Close()
		limit := a.maxPreviewBytes
		data, err := io.ReadAll(io.LimitReader(body, limit+1))
		if err != nil {
			writeError(w, r, domain.NewError(domain.CodeStorageProviderError, "The transfer preview could not be read."))
			return
		}
		truncated := int64(len(data)) > limit
		if truncated {
			data = data[:limit]
		}
		preview := map[string]any{"truncated": truncated}
		if utf8.Valid(data) && (strings.HasPrefix(transfer.MediaType, "text/") || transfer.MediaType == "application/json") {
			preview["text"] = string(data)
		} else {
			preview["data_base64"] = base64.StdEncoding.EncodeToString(data)
		}
		result["preview"] = preview
		a.audit(r, "transfer.previewed", "transfer", transfer.ID, transfer.DestinationBrainID, nil, &transfer.ExecutionID, domain.AuditStatusAllowed, map[string]any{"truncated": truncated}, []string{activeUser(r.Context()).ID})
	}
	writeData(w, r, http.StatusOK, result)
}

func (a *API) visibleTransfer(r *http.Request) (domain.Transfer, bool, error) {
	transfer, err := a.store.GetTransfer(r.Context(), r.PathValue("transferId"))
	if err != nil {
		return domain.Transfer{}, false, domain.NewError(domain.CodeNodeNotFound, "The transfer does not exist.")
	}
	userID, sourceOwned, destinationOwned := activeUser(r.Context()).ID, false, false
	if transfer.SourceBrainID != nil {
		if brain, getErr := a.store.GetBrain(r.Context(), *transfer.SourceBrainID); getErr == nil {
			sourceOwned = brain.OwnerUserID == userID
		}
	}
	if transfer.DestinationBrainID != nil {
		if brain, getErr := a.store.GetBrain(r.Context(), *transfer.DestinationBrainID); getErr == nil {
			destinationOwned = brain.OwnerUserID == userID
		}
	}
	if !sourceOwned && !destinationOwned {
		return domain.Transfer{}, false, domain.NewError(domain.CodeNotAuthorized, "The transfer is not visible to this user.")
	}
	return transfer, destinationOwned, nil
}

func (a *API) acceptTransfer(w http.ResponseWriter, r *http.Request) {
	key, err := requireIdempotencyKey(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var body struct {
		ObjectKey string `json:"object_key"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	body.ObjectKey, err = assets.NormalizeObjectKey(body.ObjectKey)
	if err != nil {
		writeError(w, r, domain.NewError(domain.CodeInvalidRequest, err.Error()))
		return
	}
	requestBytes, _ := json.Marshal(body)
	record, replay, err := a.startIdempotency(r, "accept:"+r.PathValue("transferId"), key, requestBytes)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if replay != nil {
		writeData(w, r, *record.ResponseStatus, replay)
		return
	}
	transfer, destinationOwned, err := a.visibleTransfer(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !destinationOwned {
		writeError(w, r, domain.NewError(domain.CodeNotAuthorized, "Only the destination owner may accept a transfer."))
		return
	}
	if transfer.Status != domain.TransferStatusPending {
		writeError(w, r, domain.NewError(domain.CodeTransferAlreadyResolved, "The transfer has already been resolved."))
		return
	}
	if !transfer.ExpiresAt.After(a.now()) {
		writeError(w, r, domain.NewError(domain.CodeTransferExpired, "The transfer has expired."))
		return
	}
	assetID := newUUID()
	var accepted domain.Asset
	err = a.store.InTx(r.Context(), func(tx *store.Store) error {
		locked, lockErr := tx.LockTransfer(r.Context(), transfer.ID)
		if lockErr != nil {
			return lockErr
		}
		if locked.Status != domain.TransferStatusPending {
			return domain.NewError(domain.CodeTransferAlreadyResolved, "The transfer has already been resolved.")
		}
		if !locked.ExpiresAt.After(a.now()) {
			_, _ = tx.MarkTransferExpired(r.Context(), locked.ID, a.now())
			return domain.NewError(domain.CodeTransferExpired, "The transfer has expired.")
		}
		if _, collisionErr := tx.GetAssetByObjectKey(r.Context(), *locked.DestinationBrainID, body.ObjectKey); collisionErr == nil {
			return domain.NewError(domain.CodeNameAlreadyExists, "An asset already uses that object key.")
		} else if !errors.Is(collisionErr, pgx.ErrNoRows) {
			return collisionErr
		}
		source, _, getErr := a.objects.Get(r.Context(), locked.StoragePath)
		if getErr != nil {
			return getErr
		}
		defer source.Close()
		data, readErr := io.ReadAll(io.LimitReader(source, int64(a.maxRoutePayloadBytes)+1))
		if readErr != nil || len(data) > a.maxRoutePayloadBytes {
			return domain.NewError(domain.CodePayloadTooLarge, "The transfer exceeds the configured payload limit.")
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != locked.SHA256 {
			return domain.NewError(domain.CodeAssetUnavailable, "The transfer checksum did not match.")
		}
		destinationPath := "brains/" + *locked.DestinationBrainID + "/accepted-transfers/" + locked.ID + "/" + locked.SHA256
		if putErr := a.objects.Put(r.Context(), destinationPath, locked.MediaType, bytes.NewReader(data), int64(len(data)), true); putErr != nil {
			return putErr
		}
		format := domain.AssetFormatBinary
		if locked.MediaType == "text/plain" {
			format = domain.AssetFormatText
		} else if strings.Contains(locked.MediaType, "markdown") {
			format = domain.AssetFormatMarkdown
		} else if strings.Contains(locked.MediaType, "csv") {
			format = domain.AssetFormatCSV
		}
		checksum := locked.SHA256
		accepted, lockErr = tx.InsertAsset(r.Context(), store.AssetInput{ID: assetID, BrainID: *locked.DestinationBrainID, ObjectKey: body.ObjectKey, StoragePath: destinationPath, OriginalFilename: filepath.Base(locked.SuggestedFilename), MediaType: locked.MediaType, ByteSize: locked.ByteSize, SHA256: &checksum, Format: format, ProcessingState: domain.AssetStateReady})
		if lockErr != nil {
			return lockErr
		}
		_, lockErr = tx.MarkTransferAccepted(r.Context(), locked.ID, accepted.ID, a.now())
		return lockErr
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	_ = a.objects.Delete(r.Context(), []string{transfer.StoragePath})
	result := map[string]any{"transfer_id": transfer.ID, "status": "accepted", "asset": accepted}
	responseBytes, _ := json.Marshal(result)
	_, _ = a.store.CompleteIdempotencyRecord(r.Context(), record.ID, http.StatusOK, responseBytes)
	a.audit(r, "transfer.accepted", "transfer", transfer.ID, transfer.DestinationBrainID, nil, &transfer.ExecutionID, domain.AuditStatusSucceeded, map[string]any{"accepted_asset_id": accepted.ID}, []string{activeUser(r.Context()).ID})
	writeData(w, r, http.StatusOK, result)
}

func (a *API) rejectTransfer(w http.ResponseWriter, r *http.Request) {
	key, err := requireIdempotencyKey(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	requestBytes := []byte(`{}`)
	record, replay, err := a.startIdempotency(r, "reject:"+r.PathValue("transferId"), key, requestBytes)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if replay != nil {
		writeData(w, r, *record.ResponseStatus, replay)
		return
	}
	transfer, destinationOwned, err := a.visibleTransfer(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !destinationOwned {
		writeError(w, r, domain.NewError(domain.CodeNotAuthorized, "Only the destination owner may reject a transfer."))
		return
	}
	err = a.store.InTx(r.Context(), func(tx *store.Store) error {
		locked, lockErr := tx.LockTransfer(r.Context(), transfer.ID)
		if lockErr != nil {
			return lockErr
		}
		if locked.Status != domain.TransferStatusPending {
			return domain.NewError(domain.CodeTransferAlreadyResolved, "The transfer has already been resolved.")
		}
		_, lockErr = tx.MarkTransferRejected(r.Context(), locked.ID, a.now())
		return lockErr
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	_ = a.objects.Delete(r.Context(), []string{transfer.StoragePath})
	result := map[string]any{"transfer_id": transfer.ID, "status": "rejected"}
	responseBytes, _ := json.Marshal(result)
	_, _ = a.store.CompleteIdempotencyRecord(r.Context(), record.ID, http.StatusOK, responseBytes)
	a.audit(r, "transfer.rejected", "transfer", transfer.ID, transfer.DestinationBrainID, nil, &transfer.ExecutionID, domain.AuditStatusSucceeded, nil, []string{activeUser(r.Context()).ID})
	writeData(w, r, http.StatusOK, result)
}
