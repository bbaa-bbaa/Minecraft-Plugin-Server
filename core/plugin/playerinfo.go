package plugin

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"cgit.bbaa.fun/bbaa/minecraft-plugin-server/core/plugin/pluginabi"
	"github.com/samber/lo"
)

type MinecraftPlayerInfo struct {
	Player       string
	Location     *MinecraftPosition
	LastLocation *MinecraftPosition
	UUID         string
	Extra        map[string]any
	extraLock    sync.RWMutex
	playerInfo   *PlayerInfo
}

func (mpi *MinecraftPlayerInfo) Commit() error {
	return mpi.playerInfo.Commit(mpi)
}

func (mpi *MinecraftPlayerInfo) GetExtra(context pluginabi.PluginName) (extra any) {
	mpi.extraLock.RLock()
	defer mpi.extraLock.RUnlock()
	if extra, ok := mpi.Extra[context.Name()]; ok {
		return extra
	}
	return nil
}

func (mpi *MinecraftPlayerInfo) PutExtra(context pluginabi.PluginName, extra any) {
	mpi.extraLock.Lock()
	defer mpi.extraLock.Unlock()
	mpi.Extra[context.Name()] = extra
}

type PlayerInfo struct {
	BasePlugin
	updateTicker   *time.Ticker
	playerList     []string
	playerInfo     map[string]*MinecraftPlayerInfo
	playerInfoLock sync.RWMutex
}

var PlayerEnterLeaveMessage = regexp.MustCompile(`(left|joined) the game`)

func (pi *PlayerInfo) Init(pm pluginabi.PluginManager) error {
	pi.BasePlugin.Init(pm, pi)
	pi.playerInfo = make(map[string]*MinecraftPlayerInfo)
	pm.RegisterLogProcesser(pi, pi.playerJoinLeaveEvent)
	return nil
}

func (pi *PlayerInfo) playerJoinLeaveEvent(log string) {
	if PlayerEnterLeaveMessage.MatchString(log) {
		pi.updatePlayerList()
	}
}

func (pi *PlayerInfo) convertUUID(rawData []int32) (uuid string, err error) {
	fmt.Println(rawData)
	if len(rawData) != 4 {
		return "", fmt.Errorf("parse UUID 失败")
	}
	rawUUID := &bytes.Buffer{}
	binary.Write(rawUUID, binary.BigEndian, rawData)
	hexUUID := fmt.Sprintf("%x", rawUUID.Bytes())
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexUUID[0:8], hexUUID[8:12], hexUUID[12:16], hexUUID[16:20], hexUUID[20:]), nil
}

func (pi *PlayerInfo) getPlayerUUID(player string) (uuid string, err error) {
	uuidEntityData := pi.RunCommand("data get entity " + player + " UUID")
	uuidData := strings.SplitN(uuidEntityData, ":", 2)
	if len(uuidData) != 2 {
		return "", fmt.Errorf("获取 NBT 失败")
	}
	uuidNbt := strings.TrimSpace(uuidData[1])
	var tempInt int64
	uuidIntArray := lo.Map(strings.Split(strings.TrimSpace(strings.TrimRight(uuidNbt[strings.Index(uuidNbt, ";")+1:], "]")), ","), func(item string, index int) int32 {
		if err != nil {
			return 0
		}
		tempInt, err = strconv.ParseInt(strings.TrimSpace(item), 10, 32)
		return int32(tempInt)
	})
	if err != nil {
		return "", err
	}
	return pi.convertUUID(uuidIntArray)
}

func (pi *PlayerInfo) getPlayerPosition(player string) (position *MinecraftPosition, err error) {
	var tempFloat float64
	entityPosRes := pi.RunCommand("data get entity " + player + " Pos")
	entityPos := strings.SplitN(entityPosRes, ":", 2)
	if len(entityPos) != 2 {
		return nil, fmt.Errorf("获取 NBT 失败")
	}
	entityPosList := lo.Map(strings.Split(strings.Trim(entityPos[1], "[ ]"), ","), func(item string, index int) float64 {
		if err != nil {
			return 0
		}
		tempFloat, err = strconv.ParseFloat(strings.Trim(item, " d"), 64)
		return tempFloat
	})
	if err != nil {
		return nil, err
	}
	position = &MinecraftPosition{Position: [3]float64(entityPosList)}
	entityDimRes := pi.RunCommand("data get entity " + player + " Dimension")
	entityDim := strings.SplitN(entityDimRes, ":", 2)
	if len(entityDim) != 2 {
		return nil, fmt.Errorf("获取 NBT 失败")
	}
	position.Dimension = strings.Trim(entityDim[1], `" `)
	return position, err
}

func (pi *PlayerInfo) GetPlayerInfo(player string) (playerInfo *MinecraftPlayerInfo, err error) {
	var ok bool
	pi.playerInfoLock.RLock()
	if !slices.Contains(pi.playerList, player) {
		pi.playerInfoLock.RUnlock()
		return nil, fmt.Errorf("玩家不存在")
	}
	if playerInfo, ok = pi.playerInfo[player]; !ok {
		pi.playerInfoLock.RUnlock()
		playerInfo = &MinecraftPlayerInfo{Player: player, playerInfo: pi}
		pi.playerInfoLock.Lock()
		pi.playerInfo[player] = playerInfo
		pi.playerInfoLock.Unlock()
	} else {
		pi.playerInfoLock.RUnlock()
	}
	if playerInfo.UUID == "" {
		playerInfo.UUID, err = pi.getPlayerUUID(player)
		if err != nil {
			return nil, err
		}
	}
	playerInfo.Location, err = pi.getPlayerPosition(player)
	if err != nil {
		return nil, err
	}
	return playerInfo, nil
}

func (pi *PlayerInfo) updatePlayerList() {
	playerlistMsg := pi.RunCommand("list")
	playerlistSplitText := strings.SplitN(playerlistMsg, ":", 2)
	if len(playerlistSplitText) == 2 {
		playerList := strings.Split(strings.TrimSpace(playerlistSplitText[1]), ",")
		pi.playerList = lo.Map(playerList, func(players string, index int) string {
			return strings.TrimSpace(players)
		})
	}
}

func (pi *PlayerInfo) updatePlayerWorker() {
	for range pi.updateTicker.C {
		pi.updatePlayerList()
	}
}

func (pi *PlayerInfo) Start() {
	createWorker := false
	if pi.updateTicker == nil {
		createWorker = true
	}
	pi.updateTicker = time.NewTicker(60 * time.Second)
	pi.updatePlayerList()
	if createWorker {
		go pi.updatePlayerWorker()
	}
}

func (pi *PlayerInfo) Pause() {
	pi.updateTicker.Stop()
}

func (pi *PlayerInfo) Name() string {
	return "PlayerInfo"
}

func (pi *PlayerInfo) DisplayName() string {
	return "玩家信息"
}

func (pi *PlayerInfo) Commit(mpi *MinecraftPlayerInfo) error {
	if mpi == nil {
		return fmt.Errorf("无玩家信息")
	}
	pi.playerInfoLock.RLock()
	if !slices.Contains(pi.playerList, mpi.Player) {
		pi.playerInfoLock.RUnlock()
		return fmt.Errorf("无玩家信息")
	}
	saveData, err := json.Marshal(pi.playerInfo)
	pi.playerInfoLock.RUnlock()
	if err != nil {
		return err
	}
	return os.WriteFile("data/playerinfo.json", saveData, 0644)
}