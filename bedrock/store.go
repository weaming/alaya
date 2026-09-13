// Package bedrock 实现不可变的基岩事件日志：内容寻址、per-scope 哈希链、只追加。
//
// 它是 Alaya 的真源。上层的一切（检索、装配、乃至未来的蒸馏与推断）都是它的派生物，
// 可以随时重建，而它自身只增长、不修改。
package bedrock

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/weaming/alaya/config"
	"github.com/weaming/alaya/models"
)

const eventsFileName = "events.jsonl"

// lockFileName 是数据目录内的跨进程写锁，属常驻文件，不应被当作临时文件清理。
const lockFileName = ".lock"

// lockRetries 限制锁文件再校验的重试次数。正常情况下一次即过；
// 反复失败意味着有进程在持续删除或替换它。
const lockRetries = 3

// chainState 是某个 scope 的链尾状态。链是 per-scope 的局部链（04 §3.2），
// 而非一条全局链——否则并发写者只能串行化。
type chainState struct {
	lastLink string
	lastSeq  int64
}

// fileFingerprint 用于判断日志是否被外部改动过。
//
// 尺寸能捕获追加与截断，修改时间能捕获等长重写——只有尺寸时，
// 一次等长的内容替换不会被察觉，本进程会继续基于过期的链尾追加。
type fileFingerprint struct {
	size    int64
	modTime time.Time
}

func fingerprintOf(info os.FileInfo) fileFingerprint {
	return fileFingerprint{size: info.Size(), modTime: info.ModTime()}
}

// Store 管理基岩事件日志。一切写入只追加，加载时丢弃崩溃残留的半行。
type Store struct {
	dir  string
	path string

	mu     sync.Mutex
	file   *os.File
	lock   *os.File
	heads  map[string]chainState
	events []models.Event
	byID   map[string]int

	truncatedTail bool
	loaded        fileFingerprint

	// tailPending 表示加载时发现尾部残行但未修复。
	// 无锁加载不能改文件——那半行可能是另一个进程正在写的。
	tailPending bool

	// broken 非 nil 表示数据目录在运行期间损坏且无法重载，
	// 此后的写入一律拒绝，避免基于过期链尾继续追加而写坏链。
	broken error
}

// Open 打开（必要时创建）数据目录并读取全部事件。
//
// 刻意不取锁：查询与校验不需要写权限，只读挂载、备份副本与容器只读卷下也应可用。
// 代价是加载可能与其它进程的写入并发，因此只读到最后一个完整行为止，
// 并且不修改文件——尾部残行留待下次写入时在锁内修复。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录 %s: %w", dir, err)
	}

	store := &Store{
		dir:   dir,
		path:  filepath.Join(dir, eventsFileName),
		heads: make(map[string]chainState),
		byID:  make(map[string]int),
	}

	if err := store.load(false); err != nil {
		return nil, err
	}

	return store, nil
}

// load 读取全部事件并重建链尾与索引。
//
// 区分两类损坏：尾部的半行是崩溃残留，截断即可恢复；
// 中间出现的解析失败则是篡改或磁盘损坏，必须报错而非静默丢弃后续内容。
//
// repair 为 false 时不修改文件（无锁加载），残行只被忽略并记为待修复。
func (s *Store) load(repair bool) error {
	// 先取指纹再读内容，顺序不可颠倒。
	//
	// 无锁加载时若有写者恰好插进这两步之间：先读后 stat 会让指纹描述
	// 比已解析内容更新的文件状态，后续比对便误判为"未变化"，
	// 于是沿用过期链尾追加，写出重复序号；先 stat 后读则指纹停在旧状态，
	// 最坏是多一次重载——宁可多，不可少。
	info, statErr := os.Stat(s.path)

	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取事件日志: %w", err)
	}

	var (
		offset int
		lineNo int
	)

	for offset < len(raw) {
		rest := raw[offset:]
		end := bytes.IndexByte(rest, '\n')
		if end < 0 {
			break // 尾部半行：进程在写入过程中退出，截断到此处
		}

		lineNo++
		line := bytes.TrimSpace(rest[:end])
		offset += end + 1

		if len(line) == 0 {
			continue
		}

		var event models.Event
		if err := json.Unmarshal(line, &event); err != nil {
			return fmt.Errorf("第 %d 行解析失败，疑似损坏或篡改（未截断，请人工检查）: %w", lineNo, err)
		}

		if err := s.adopt(event, lineNo); err != nil {
			return err
		}
	}

	if offset < len(raw) {
		if !repair {
			s.tailPending = true
		} else {
			if err := os.Truncate(s.path, int64(offset)); err != nil {
				return fmt.Errorf("截断残留半行: %w", err)
			}
			s.truncatedTail = true
		}
	}

	// 防御：指纹不得比已解析内容更新。若 stat 看到的比读到的还大，
	// 说明两步之间发生过写入，该指纹会让后续比对误判——丢弃它，
	// 让下次写入强制重载。
	if statErr == nil && info.Size() <= int64(len(raw)) {
		s.loaded = fingerprintOf(info)
	} else {
		s.loaded = fileFingerprint{}
	}

	return nil
}

// lockFile 获取数据目录的排他锁，使跨进程写入串行化。
//
// 没有它时，长驻的 MCP server 与 CLI 会各自基于过期的链尾追加，
// 写出重复序号并使整份日志无法通过校验。
//
// 持锁后必须复核锁文件身份：`.lock` 若在两次写入之间被删除或替换
// （`rm -rf ~/.alaya/*` 之类的重置动作都会），原 fd 锁住的是已被 unlink 的旧 inode，
// 而新进程创建的是新 inode——两把锁互不相干，双方都以为自己独占。
func (s *Store) lockFile() (func(), error) {
	for attempt := 0; attempt < lockRetries; attempt++ {
		if s.lock == nil {
			file, err := os.OpenFile(filepath.Join(s.dir, lockFileName), os.O_CREATE|os.O_RDWR, 0o644)
			if err != nil {
				return nil, fmt.Errorf("打开锁文件: %w", err)
			}
			s.lock = file
		}

		if err := syscall.Flock(int(s.lock.Fd()), syscall.LOCK_EX); err != nil {
			return nil, fmt.Errorf("锁定数据目录: %w", err)
		}

		if s.lockFileIntact() {
			held := s.lock

			return func() {
				_ = syscall.Flock(int(held.Fd()), syscall.LOCK_UN)
			}, nil
		}

		// 锁文件已被删除或替换：丢弃旧 fd 重新来过
		_ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
		_ = s.lock.Close()
		s.lock = nil
	}

	return nil, fmt.Errorf("锁文件 %s 反复被删除或替换，放弃获取",
		filepath.Join(s.dir, lockFileName))
}

// lockFileIntact 复核已持有的锁仍对应磁盘上的那个文件。
func (s *Store) lockFileIntact() bool {
	held, err := s.lock.Stat()
	if err != nil {
		return false
	}

	onDisk, err := os.Stat(filepath.Join(s.dir, lockFileName))
	if err != nil {
		return false
	}

	return os.SameFile(held, onDisk)
}

// refreshLocked 在锁内复核日志是否被外部改动过。
//
// 只要数据目录在进程存活期间变过——并发写入、删库重来、从备份恢复——
// 内存链尾即告失效。不复核的话，此后每次写入都会写坏链，
// 而链校验失败会让整份日志拒绝加载，等于整个记忆库报废。
func (s *Store) refreshLocked() error {
	if s.broken != nil {
		return s.broken
	}

	info, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.reset()
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查事件日志: %w", err)
	}

	// 尾部残行必须在锁内修掉，否则新事件会接在半行之后，形成无法解析的一行
	if !s.tailPending && fingerprintOf(info) == s.loaded {
		return nil
	}

	if err := s.reload(); err != nil {
		s.broken = err
		return err
	}

	return nil
}

// reload 从磁盘重新加载全部状态；失败时保持原状态不变，
// 使调用方仍能读到损坏前的视图，而不是面对一个被静默清空的库。
func (s *Store) reload() error {
	reloaded := &Store{
		dir:   s.dir,
		path:  s.path,
		heads: make(map[string]chainState),
		byID:  make(map[string]int),
	}

	if err := reloaded.load(true); err != nil {
		return err
	}

	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}

	s.heads = reloaded.heads
	s.events = reloaded.events
	s.byID = reloaded.byID
	s.loaded = reloaded.loaded
	s.truncatedTail = reloaded.truncatedTail
	s.tailPending = false

	return nil
}

// reset 丢弃全部内存状态并关闭文件句柄，为重新加载做准备。
func (s *Store) reset() {
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}

	s.heads = make(map[string]chainState)
	s.byID = make(map[string]int)
	s.events = nil
	s.loaded = fileFingerprint{}
	s.truncatedTail = false
	s.tailPending = false
}

// adopt 校验一条已解析事件并纳入内存状态。
func (s *Store) adopt(event models.Event, lineNo int) error {
	expectedID, err := contentID(event)
	if err != nil {
		return err
	}
	if event.ID != expectedID {
		return fmt.Errorf("第 %d 行内容哈希不匹配，事件已被篡改", lineNo)
	}

	head := s.heads[event.Scope]
	if event.Seq != head.lastSeq+1 {
		return fmt.Errorf("第 %d 行 scope %s 的序号为 %d，期望 %d，链已断裂",
			lineNo, event.Scope, event.Seq, head.lastSeq+1)
	}
	if event.Prev != head.lastLink {
		return fmt.Errorf("第 %d 行 scope %s 的前序哈希不匹配，链已断裂", lineNo, event.Scope)
	}
	if expected := chainLink(event.ID, event.Prev, event.Seq); event.Link != expected {
		return fmt.Errorf("第 %d 行链哈希不匹配，事件已被篡改", lineNo)
	}

	if _, exists := s.byID[event.ID]; exists {
		return nil // 幂等写入产生的重复行，保留但不再登记
	}

	s.events = append(s.events, event)
	s.byID[event.ID] = len(s.events) - 1
	s.heads[event.Scope] = chainState{lastLink: event.Link, lastSeq: event.Seq}

	return nil
}

// Append 追加一条事件并返回落盘后的完整记录。
//
// 第二返回值为 false 表示该内容已存在（同 id），未重复写入——
// 这是内容寻址提供的幂等保证。
func (s *Store) Append(input models.Event) (models.Event, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	release, err := s.lockFile()
	if err != nil {
		return models.Event{}, false, fmt.Errorf("获取写锁（数据目录需可写）: %w", err)
	}
	defer release()

	if err := s.refreshLocked(); err != nil {
		return models.Event{}, false, err
	}

	event := input.Normalize()

	id, err := contentID(event)
	if err != nil {
		return models.Event{}, false, err
	}
	event.ID = id

	if idx, ok := s.byID[id]; ok {
		return s.events[idx], false, nil
	}

	head := s.heads[event.Scope]
	event.Prev = head.lastLink
	event.Seq = head.lastSeq + 1
	event.Link = chainLink(event.ID, event.Prev, event.Seq)
	event.RecordedAt = config.NowISO()
	event.ObservedAt = event.EffectiveObservedAt()

	written, err := s.writeLine(event)
	if err != nil {
		return models.Event{}, false, err
	}

	s.events = append(s.events, event)
	s.byID[event.ID] = len(s.events) - 1
	s.heads[event.Scope] = chainState{lastLink: event.Link, lastSeq: event.Seq}

	if info, err := os.Stat(s.path); err == nil {
		s.loaded = fingerprintOf(info)
	} else {
		s.loaded.size += int64(written)
	}

	return event, true, nil
}

func (s *Store) writeLine(event models.Event) (int, error) {
	file, err := s.fileLocked()
	if err != nil {
		return 0, err
	}

	line, err := json.Marshal(event)
	if err != nil {
		return 0, fmt.Errorf("编码事件: %w", err)
	}

	payload := append(line, '\n')

	if _, err := file.Write(payload); err != nil {
		return 0, fmt.Errorf("写入事件: %w", err)
	}

	// 立即落盘：记忆的写入必须能抗进程崩溃，否则记录的"发生过"不可信
	if err := file.Sync(); err != nil {
		return 0, fmt.Errorf("同步事件日志: %w", err)
	}

	return len(payload), nil
}

// fileLocked 返回常驻的文件句柄，避免每条事件重复打开文件。
func (s *Store) fileLocked() (*os.File, error) {
	if s.file != nil {
		return s.file, nil
	}

	file, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("打开事件日志 %s: %w", s.path, err)
	}
	s.file = file

	return file, nil
}

// Events 返回全部事件的副本。
func (s *Store) Events() []models.Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]models.Event, len(s.events))
	copy(out, s.events)

	return out
}

// Exists 报告给定内容 id 的事件是否存在。
func (s *Store) Exists(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.byID[id]

	return ok
}

// TruncatedTail 报告加载时是否丢弃过崩溃残留的半行。
func (s *Store) TruncatedTail() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.truncatedTail
}

// TailPending 报告加载时发现过尾部残行但未修复（无锁加载时如此）。
func (s *Store) TailPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.tailPending
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.file == nil {
		return nil
	}

	if err := s.file.Close(); err != nil {
		return fmt.Errorf("关闭事件日志: %w", err)
	}
	s.file = nil

	if s.lock != nil {
		if err := s.lock.Close(); err != nil {
			return fmt.Errorf("关闭锁文件: %w", err)
		}
		s.lock = nil
	}

	return nil
}
