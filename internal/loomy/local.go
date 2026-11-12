package loomy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 本机桌面端落盘路径（生产环境）；测试用临时目录覆盖。
var (
	sessionGlob = `C:\Users\Public\Loomy\*\userData\auth-session.json`
	roamingDir  = func() string { return os.Getenv("APPDATA") }
)

// LocalSession 是本机 Loomy 桌面端的登录会话（auth-session.json）。
type LocalSession struct {
	Session string `json:"session"`
	UserID  string `json:"userid"`
	Phone   string `json:"phone"`
}

// FindLocalSession 供面板导入入口调用（面板仅本机可触发）。
func FindLocalSession() (LocalSession, error) { return findLocalSession() }

// findLocalSession 在本机已登录的 Loomy 桌面端安装中查找登录会话。
// Loomy 登录依赖短信验证与 HMAC 签名，桌面端之外无法自行登录，
// 因此会话来源只有桌面端落盘文件与用户粘贴两种。
//
// 多个用户目录（C:\Users\Public\Loomy\<哈希>\）并存时，按文件修改时间倒序
// 遍历，优先取最近写入的一份：桌面端重新登录会覆盖同一份 auth-session.json，
// 但换号/重装可能残留旧目录，若仍按 glob 字典序取第一个，会选到已失效的旧
// 会话，导致服务端判定登录失效（业务码 100002）。前端无效文件时继续回退。
func findLocalSession() (LocalSession, error) {
	paths, _ := filepath.Glob(sessionGlob)
	type candidate struct {
		path string
		mod  time.Time
	}
	candidates := make([]candidate, 0, len(paths))
	for _, path := range paths {
		candidates = append(candidates, candidate{path: path, mod: fileModTime(path)})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].mod.After(candidates[j].mod) })
	for _, c := range candidates {
		raw, err := os.ReadFile(c.path)
		if err != nil || len(raw) > 64<<10 {
			continue
		}
		var s LocalSession
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		if strings.TrimSpace(s.Session) != "" && strings.TrimSpace(s.UserID) != "" {
			return s, nil
		}
	}
	return LocalSession{}, fmt.Errorf("本机未找到已登录的 Loomy 桌面端，请先在桌面端登录或改用粘贴凭证")
}

// fileModTime 返回文件修改时间；读取失败时返回零值（排序时排在最后）。
func fileModTime(path string) time.Time {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}

// localDaily 是本机桌面端记录的当日宠物打工进度（pet-work.json 的
// users.<sha256(userid)>.daily；面板只信这份本地事实，不越权申报档位）。
type localDaily struct {
	BusinessDate string
	PetWorkScore float64
}

// localPetWork 读取指定账号的本机当日使用进度。
func localPetWork(userID string) (localDaily, error) {
	path := filepath.Join(roamingDir(), "Loomy", "pet-work.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return localDaily{}, fmt.Errorf("本机未找到 Loomy 桌面端使用记录（pet-work.json）")
		}
		return localDaily{}, fmt.Errorf("读取 Loomy 宠物打工记录失败")
	}
	if len(raw) > 8<<20 {
		return localDaily{}, fmt.Errorf("Loomy 宠物打工记录超出大小限制")
	}
	var root struct {
		PetWork struct {
			Users map[string]struct {
				Daily struct {
					BusinessDate string  `json:"businessDate"`
					PetWorkScore float64 `json:"petWorkScore"`
				} `json:"daily"`
			} `json:"users"`
		} `json:"petWork"`
	}
	if json.Unmarshal(raw, &root) != nil {
		return localDaily{}, fmt.Errorf("Loomy 宠物打工记录格式无效")
	}
	entry, ok := root.PetWork.Users[userKey(userID)]
	if !ok || entry.Daily.BusinessDate == "" {
		return localDaily{}, fmt.Errorf("本机暂无该账号的宠物打工记录，请先在桌面端使用")
	}
	return localDaily{BusinessDate: entry.Daily.BusinessDate, PetWorkScore: entry.Daily.PetWorkScore}, nil
}

// userKey 复刻桌面端：宠物打工本地状态按 sha256(userid) 全量十六进制分桶。
func userKey(userID string) string {
	sum := sha256.Sum256([]byte(userID))
	return hex.EncodeToString(sum[:])
}
