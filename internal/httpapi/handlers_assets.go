package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"secure-brain/internal/assets"
	"secure-brain/internal/domain"
	"secure-brain/internal/store"
)

func (a *API) listAssets(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	items, err := a.store.ListAssets(r.Context(), brain.ID, 50)
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	writeData(w, r, http.StatusOK, items)
}

func (a *API) getAsset(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	asset, err := a.store.GetAssetInBrain(r.Context(), brain.ID, recordIDAtBoundary(r.PathValue("assetId")))
	if err != nil {
		writeError(w, r, domain.NewError(domain.CodeNodeNotFound, "The asset does not exist."))
		return
	}
	writeData(w, r, http.StatusOK, asset)
}

func (a *API) uploadAsset(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if a.objects == nil {
		writeError(w, r, domain.NewError(domain.CodeStorageProviderError, "Storage is unavailable."))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.maxFileBytes+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, r, domain.NewError(domain.CodeInvalidRequest, "A multipart upload is required."))
		return
	}
	temp, err := os.CreateTemp("", "securebrain-upload-*")
	if err != nil {
		writeError(w, r, domain.NewError(domain.CodeStorageProviderError, "The upload could not be staged."))
		return
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	defer temp.Close()
	var objectKeyText, filename, clientMediaType string
	var fileSeen bool
	overwrite, _ := strconv.ParseBool(r.URL.Query().Get("overwrite"))
	hash := sha256.New()
	var size int64
	for {
		part, partErr := reader.NextPart()
		if errors.Is(partErr, io.EOF) {
			break
		}
		if partErr != nil {
			writeError(w, r, domain.NewError(domain.CodeInvalidRequest, "The multipart upload is invalid."))
			return
		}
		switch part.FormName() {
		case "object_key":
			value, readErr := io.ReadAll(io.LimitReader(part, 514))
			if readErr != nil || len(value) > 512 {
				part.Close()
				writeError(w, r, domain.NewError(domain.CodeInvalidRequest, "The object key is invalid."))
				return
			}
			objectKeyText = string(value)
		case "file":
			if fileSeen {
				part.Close()
				writeError(w, r, domain.NewError(domain.CodeInvalidRequest, "Exactly one file is required."))
				return
			}
			fileSeen, filename, clientMediaType = true, filepath.Base(part.FileName()), part.Header.Get("Content-Type")
			written, copyErr := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(part, a.maxFileBytes+1))
			size = written
			if copyErr != nil {
				part.Close()
				writeError(w, r, domain.NewError(domain.CodeInvalidRequest, "The file could not be read."))
				return
			}
		case "overwrite":
			value, _ := io.ReadAll(io.LimitReader(part, 6))
			parsed, parseErr := strconv.ParseBool(strings.TrimSpace(string(value)))
			if parseErr != nil {
				part.Close()
				writeError(w, r, domain.NewError(domain.CodeInvalidRequest, "overwrite must be true or false."))
				return
			}
			overwrite = parsed
		default:
			_, _ = io.Copy(io.Discard, io.LimitReader(part, 1024))
		}
		part.Close()
	}
	if !fileSeen || filename == "" {
		writeError(w, r, domain.NewError(domain.CodeInvalidRequest, "Exactly one named file is required."))
		return
	}
	if size > a.maxFileBytes {
		writeError(w, r, domain.NewError(domain.CodePayloadTooLarge, "The file exceeds the upload size limit."))
		return
	}
	objectKey, err := assets.NormalizeObjectKey(objectKeyText)
	if err != nil {
		writeError(w, r, domain.NewError(domain.CodeInvalidRequest, err.Error()))
		return
	}
	existing, existingErr := a.store.GetAssetByObjectKey(r.Context(), brain.ID, objectKey)
	if existingErr == nil && !overwrite {
		writeError(w, r, domain.NewError(domain.CodeNameAlreadyExists, "An asset already uses that object key; set overwrite=true to replace it."))
		return
	}
	if existingErr != nil && !errors.Is(existingErr, pgx.ErrNoRows) {
		writeError(w, r, databaseError(existingErr))
		return
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	bytes, err := io.ReadAll(io.LimitReader(temp, a.maxFileBytes+1))
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	classification := assets.Classify(filename, clientMediaType, bytes)
	checksum := hex.EncodeToString(hash.Sum(nil))
	assetID := existing.ID
	if existingErr != nil {
		assetID = newUUID()
	}
	storagePath := fmt.Sprintf("brains/%s/assets/%s/%s", brain.ID, assetID, checksum)
	parseError := (*string)(nil)
	if classification.ParseError != "" {
		parseError = &classification.ParseError
	}
	input := store.AssetInput{ID: assetID, BrainID: brain.ID, ObjectKey: objectKey, StoragePath: storagePath, OriginalFilename: filename, MediaType: classification.MediaType, ByteSize: size, SHA256: &checksum, Format: classification.Format, ProcessingState: domain.AssetStateUploading, ParseError: parseError}
	if existingErr != nil {
		if _, err := a.store.InsertAsset(r.Context(), input); err != nil {
			writeError(w, r, databaseError(err))
			return
		}
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	upsert := existingErr == nil && existing.StoragePath == storagePath
	if err := a.objects.Put(r.Context(), storagePath, classification.MediaType, temp, size, upsert); err != nil {
		if existingErr != nil {
			input.ProcessingState = domain.AssetStateUploadFailed
			_, _ = a.store.UpdateAsset(r.Context(), input)
		}
		writeError(w, r, err)
		return
	}
	input.ProcessingState = classification.ProcessingState
	result, err := a.store.UpdateAsset(r.Context(), input)
	if err != nil {
		_ = a.objects.Delete(r.Context(), []string{storagePath})
		writeError(w, r, databaseError(err))
		return
	}
	if existingErr == nil && existing.StoragePath != storagePath {
		_ = a.objects.Delete(r.Context(), []string{existing.StoragePath})
	}
	status := http.StatusCreated
	eventType := "asset.uploaded"
	if existingErr == nil {
		status = http.StatusOK
		eventType = "asset.overwritten"
	}
	a.audit(r, eventType, "asset", result.ID, &brain.ID, nil, nil, domain.AuditStatusSucceeded, map[string]any{"object_key": result.ObjectKey, "byte_size": result.ByteSize, "sha256": result.SHA256}, []domain.RecordID{brain.OwnerUserID})
	if result.ProcessingState == domain.AssetStateParseFailed {
		a.audit(r, "asset.parse_failed", "asset", result.ID, &brain.ID, nil, nil, domain.AuditStatusFailed, map[string]any{"object_key": result.ObjectKey}, []domain.RecordID{brain.OwnerUserID})
	}
	writeData(w, r, status, result)
}

func (a *API) assetContent(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	asset, err := a.store.GetAssetInBrain(r.Context(), brain.ID, recordIDAtBoundary(r.PathValue("assetId")))
	if err != nil {
		writeError(w, r, domain.NewError(domain.CodeNodeNotFound, "The asset does not exist."))
		return
	}
	body, _, err := a.objects.Get(r.Context(), asset.StoragePath)
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer body.Close()
	if r.URL.Query().Get("download") == "true" {
		w.Header().Set("Content-Type", asset.MediaType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(asset.OriginalFilename)}))
		w.Header().Set("Content-Length", strconv.FormatInt(asset.ByteSize, 10))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, io.LimitReader(body, asset.ByteSize))
		return
	}
	data, err := io.ReadAll(io.LimitReader(body, a.maxFileBytes+1))
	if err != nil {
		writeError(w, r, domain.NewError(domain.CodeStorageProviderError, "The asset could not be read."))
		return
	}
	preview, err := assets.BuildPreview(asset.Format, asset.ProcessingState, data, int(a.maxPreviewBytes), min(a.maxCSVRows, 100))
	if err != nil {
		if asset.ProcessingState == domain.AssetStateParseFailed {
			preview = assets.Preview{Kind: "binary"}
		} else {
			writeError(w, r, domain.NewError(domain.CodeAssetParseFailed, "The asset cannot be previewed as structured data."))
			return
		}
	}
	writeData(w, r, http.StatusOK, map[string]any{"asset": asset, "preview": preview})
}

func (a *API) deleteAsset(w http.ResponseWriter, r *http.Request) {
	brain, err := a.ownerBrain(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	asset, err := a.store.GetAssetInBrain(r.Context(), brain.ID, recordIDAtBoundary(r.PathValue("assetId")))
	if err != nil {
		writeError(w, r, domain.NewError(domain.CodeNodeNotFound, "The asset does not exist."))
		return
	}
	inUse, err := a.store.AssetReferencedByEnabledPath(r.Context(), asset.ID)
	if err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	if inUse {
		writeError(w, r, domain.NewError(domain.CodeResourceInUse, "The asset is referenced by an enabled query path."))
		return
	}
	if _, err := a.store.DeleteAsset(r.Context(), brain.ID, asset.ID); err != nil {
		writeError(w, r, databaseError(err))
		return
	}
	_ = a.objects.Delete(r.Context(), []string{asset.StoragePath})
	a.audit(r, "asset.deleted", "asset", asset.ID, &brain.ID, nil, nil, domain.AuditStatusSucceeded, map[string]any{"object_key": asset.ObjectKey}, []domain.RecordID{brain.OwnerUserID})
	w.WriteHeader(http.StatusNoContent)
}

func newUUID() domain.RecordID {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("crypto/rand unavailable")
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	h := hex.EncodeToString(value[:])
	id, err := domain.ParseRecordID(strings.Join([]string{h[:8], h[8:12], h[12:16], h[16:20], h[20:]}, "-"))
	if err != nil {
		panic("generated invalid UUID")
	}
	return id
}
