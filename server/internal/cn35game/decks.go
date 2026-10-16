// decks.go MIN350-06：账号编队领域契约与客户端 wire 顺序。
package cn35game

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

const (
	DefaultDeckCount = 2
	DefaultDeckSlots = 5
)

var (
	ErrDeckNotFound     = errors.New("cn35game: 编队不存在或 ordinal 非法")
	ErrDeckSlotInvalid  = errors.New("cn35game: 编队槽位非法")
	ErrDeckUnitNotOwned = errors.New("cn35game: 编队单位未由当前账号持有")
	ErrDeckDuplicate    = errors.New("cn35game: 编队包含重复单位")
	ErrDeckEmpty        = errors.New("cn35game: 空编队不能开战")
	ErrDeckUnitCount    = errors.New("cn35game: 编队人数超出关卡限制")
	ErrDeckSlotConflict = errors.New("cn35game: 编队槽位已变化")
)

// Deck 是一个账号队伍的 wire 投影。Slots 长度固定为 DefaultDeckSlots；
// 空字符串表示客户端可编辑空槽，非空项是 user_units.id 模板键。
type Deck struct {
	PartyID      string
	DisplayOrder int
	Slots        []string
}

// DeckStore 账号编队读写面；身份从解密会话取得，不接受 user_id 参数。
type DeckStore interface {
	Decks(userID string) ([]Deck, error)
	SetDeckUnit(userID, partyID string, slotIndex, unitID int) error
	ClearDeckUnit(userID, partyID string, slotIndex, expectedUnitID int) error
}

// OrderedParties marshals the parties object in display order. Unity's
// JSONObject preserves source key order when it creates the 001/002 carousel,
// while encoding/json sorts ordinary map keys. Keep this ordered type all the
// way to final response serialization.
type OrderedParties []Deck

func (p OrderedParties) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, deck := range p {
		if i > 0 {
			out.WriteByte(',')
		}
		key, err := json.Marshal(deck.PartyID)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(deck.Slots)
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// DecksWire builds the ResponseFilter-compatible data shape.
func DecksWire(decks []Deck) map[string]any {
	return map[string]any{"parties": OrderedParties(decks)}
}

// ParsePartyOrdinal maps the client wheel label (001, 002, …) to a one-based
// ordinal. It accepts numeric forms such as "1" retained by older local probes.
func ParsePartyOrdinal(raw string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%w: %q", ErrDeckNotFound, raw)
	}
	return n, nil
}
