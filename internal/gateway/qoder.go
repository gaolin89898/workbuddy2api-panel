package gateway

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5" // COSY wire signature (not password hashing).
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// COSY protocol corroborated against shuishuipingan/qoder2api-hub (MIT).
const qoderAlphabet = "_doRTgHZBKcGVjlvpC,@aFSx#DPuNJme&i*MzLOEn)sUrthbf%Y^w.(kIQyXqWA!"
const qoderPublicKey = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDA8iMH5c02LilrsERw9t6Pv5Nc
4k6Pz1EaDicBMpdpxKduSZu5OANqUq8er4GM95omAGIOPOh+Nx0spthYA2BqGz+l
6HRkPJ7S236FZz73In/KVuLnwI8JJ2CbuJap8kvheCCZpmAWpb/cPx/3Vr/J6I17
XcW+ML9FoCI6AOvOzwIDAQAB
-----END PUBLIC KEY-----`

func qoderEncode(raw []byte) string {
	s := base64.StdEncoding.EncodeToString(raw)
	n := len(s)
	a := n / 3
	s = s[n-a:] + s[a:n-a] + s[:a]
	std := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	out := []byte(s)
	for i, b := range out {
		if b == '=' {
			out[i] = '$'
		} else {
			out[i] = qoderAlphabet[strings.IndexByte(std, b)]
		}
	}
	return string(out)
}
func qoderHeaders(c Credential, body, path, model string) (http.Header, error) {
	// Native protocol uses a 16-character hex session key as both AES key and IV.
	key := []byte(strings.ReplaceAll(id(), "-", "")[:16])
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	identity := map[string]string{"name": c.Nickname, "aid": c.UID, "uid": c.UID, "yx_uid": "", "organization_id": "", "organization_name": "", "user_type": "personal_professional_trial", "security_oauth_token": c.Token, "refresh_token": c.RefreshToken}
	plain, _ := json.Marshal(identity)
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	for i := 0; i < pad; i++ {
		plain = append(plain, byte(pad))
	}
	encrypted := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, key).CryptBlocks(encrypted, plain)
	pemBlock, _ := pem.Decode([]byte(qoderPublicKey))
	pub, err := x509.ParsePKIXPublicKey(pemBlock.Bytes)
	if err != nil {
		return nil, err
	}
	wrapped, err := rsa.EncryptPKCS1v15(rand.Reader, pub.(*rsa.PublicKey), key)
	if err != nil {
		return nil, err
	}
	cosyKey := base64.StdEncoding.EncodeToString(wrapped)
	payload, _ := json.Marshal(map[string]string{"cosyVersion": "1.1.75", "ideVersion": "", "info": base64.StdEncoding.EncodeToString(encrypted), "requestId": id(), "version": "v1"})
	payload64 := base64.StdEncoding.EncodeToString(payload)
	date := strconv.FormatInt(time.Now().Unix(), 10)
	u, _ := url.Parse(path)
	signature := md5.Sum([]byte(strings.Join([]string{payload64, cosyKey, date, body, strings.TrimPrefix(u.Path, "/algo")}, "\n")))
	h := http.Header{}
	headers := map[string]string{"Content-Type": "application/json", "Accept": "text/event-stream", "Accept-Encoding": "identity", "Authorization": "Bearer COSY." + payload64 + "." + hex.EncodeToString(signature[:]), "Cosy-Key": cosyKey, "Cosy-Date": date, "Cosy-User": c.UID, "Cosy-Version": "1.1.75", "Cosy-Clienttype": "5", "Cosy-Data-Policy": "agree", "Cosy-Business-Product": "cli", "Cosy-Business-Type": "agent", "Cosy-Scene": "assistant", "Cosy-Machineid": c.MachineID, "Login-Version": "v2", "User-Agent": "undici", "Cache-Control": "no-cache"}
	machineToken := sha512.Sum512([]byte("machinetoken:" + c.UID))
	headers["Cosy-Machinetoken"] = base64.RawURLEncoding.EncodeToString(machineToken[:])[:43]
	machineType := md5.Sum([]byte("machinetype:" + c.UID))
	headers["Cosy-Machinetype"] = hex.EncodeToString(machineType[:])[:18]
	if model != "" {
		headers["X-Model-Key"] = model
		headers["X-Model-Source"] = "system"
	}
	for k, v := range headers {
		h.Set(k, v)
	}
	return h, nil
}
func qoderBody(m Model, req map[string]any, session string) map[string]any {
	mc := clone(m.wire)
	mc["key"] = m.ID
	mc["model"] = ""
	mc["format"] = "openai"
	mc["source"] = "system"
	mc["url"] = ""
	mc["api_key"] = ""
	params := map[string]any{}
	for _, k := range []string{"max_tokens", "temperature", "top_p", "stop", "reasoning_effort", "tool_choice", "parallel_tool_calls"} {
		if v, ok := req[k]; ok {
			params[k] = v
		}
	}
	if v, ok := req["max_completion_tokens"]; ok {
		params["max_tokens"] = v
	}
	tools := req["tools"]
	if tools == nil {
		tools = []any{}
	}
	messages := []any{}
	for _, v := range arr(req["messages"]) {
		message := clone(obj(v))
		if str(message["role"]) == "developer" {
			message["role"] = "system"
		}
		messages = append(messages, message)
	}
	body := map[string]any{"messages": messages, "tools": tools, "parameters": params, "model_config": mc, "request_id": id(), "request_set_id": id(), "chat_record_id": session, "session_id": session, "stream": true, "agent_id": "agent_common", "business": map[string]any{"id": session, "begin_at": time.Now().UnixMilli(), "name": "chat", "model_key": m.ID, "product": "cli", "type": "agent", "stage": "start", "version": "1.1.75"}}
	for _, k := range []string{"tool_choice", "parallel_tool_calls"} {
		if v, ok := req[k]; ok {
			body[k] = v
		}
	}
	return body
}
