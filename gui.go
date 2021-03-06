package main

import (
	"fmt"
	"github.com/gdamore/tcell"
	"github.com/rivo/tview"
	"strings"
	"time"
)

type KeyBindings map[string][]tcell.Key

var keyBindings = KeyBindings{
	"search":           {tcell.KeyF2, tcell.KeyCtrlS},
	"keys":             {tcell.KeyF3, tcell.KeyCtrlK},
	"key_list_value":   {tcell.KeyF6, tcell.KeyCtrlY},
	"key_string_value": {tcell.KeyF7, tcell.KeyCtrlA},
	"key_hash":         {tcell.KeyF6, tcell.KeyCtrlY},
	"output":           {tcell.KeyF9, tcell.KeyCtrlO},
	"command":          {tcell.KeyF1, tcell.KeyCtrlN},
	"command_focus":    {tcell.KeyF4, tcell.KeyCtrlF},
	"command_result":   {tcell.KeyF5, tcell.KeyCtrlR},
	"quit":             {tcell.KeyEsc, tcell.KeyCtrlQ},
	"switch_focus":     {tcell.KeyTab},
}

func (kb KeyBindings) SearchKey(k tcell.Key) string {
	for name, bind := range kb {
		for _, b := range bind {
			if b == k {
				return name
			}
		}
	}

	return ""
}

func (kb KeyBindings) KeyID(key string) string {
	return key
}

func (kb KeyBindings) Keys(key string) []tcell.Key {
	return kb[key]
}

func (kb KeyBindings) Name(key string) string {
	keyNames := make([]string, 0)
	for _, k := range kb[key] {
		keyNames = append(keyNames, tcell.KeyNames[k])
	}

	return strings.Join(keyNames, ", ")
}

type primitiveKey struct {
	Primitive tview.Primitive
	Key       string
}

type OutputMessage struct {
	Color   tcell.Color
	Message string
}

// RedisGli is a redis gui object
type RedisGli struct {
	metaPanel           *tview.TextView
	mainPanel           *tview.Flex
	outputPanel         *tview.List
	keyItemsPanel       *tview.List
	summaryPanel        *tview.TextView
	searchPanel         *tview.InputField
	welcomeScreen       tview.Primitive
	helpPanel           *tview.Flex
	helpMessagePanel    *tview.TextView
	helpServerInfoPanel *tview.TextView

	commandPanel       *tview.Flex
	commandFormPanel   *tview.InputField
	commandResultPanel *tview.TextView
	commandMode        bool

	leftPanel  *tview.Flex
	rightPanel *tview.Flex

	layout *tview.Flex
	pages  *tview.Pages
	app    *tview.Application

	redisClient RedisClient
	outputChan  chan OutputMessage

	itemSelectedHandler func(index int, key string) func()

	maxKeyLimit int64
	version     string
	gitCommit   string

	focusPrimitives   []primitiveKey
	currentFocusIndex int

	config Config
}

// NewRedisGli create a RedisGli object
func NewRedisGli(redisClient RedisClient, maxKeyLimit int64, version string, gitCommit string, outputChan chan OutputMessage, config Config) *RedisGli {
	gli := &RedisGli{
		redisClient:       redisClient,
		maxKeyLimit:       maxKeyLimit,
		version:           version,
		gitCommit:         gitCommit[0:8],
		focusPrimitives:   make([]primitiveKey, 0),
		currentFocusIndex: 0,
		outputChan:        outputChan,
		config:            config,
	}

	gli.welcomeScreen = tview.NewTextView().SetTitle("Hello, world!")

	gli.metaPanel = gli.createMetaPanel()
	gli.mainPanel = gli.createMainPanel()
	gli.outputPanel = gli.createOutputPanel()
	gli.summaryPanel = gli.createSummaryPanel()
	gli.keyItemsPanel = gli.createKeyItemsPanel()
	gli.itemSelectedHandler = gli.createKeySelectedHandler()
	gli.searchPanel = gli.createSearchPanel()
	gli.helpPanel = gli.createHelpPanel()

	gli.commandPanel = gli.createCommandPanel()

	gli.leftPanel = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(gli.searchPanel, 3, 0, false).
		AddItem(gli.keyItemsPanel, 0, 1, false).
		AddItem(gli.summaryPanel, 3, 1, false)

	gli.rightPanel = tview.NewFlex().SetDirection(tview.FlexRow)
	gli.redrawRightPanel(gli.mainPanel)

	gli.app = tview.NewApplication()
	gli.layout = tview.NewFlex().
		AddItem(gli.leftPanel, 0, 3, false).
		AddItem(gli.rightPanel, 0, 8, false)

	gli.focusPrimitives = append(gli.focusPrimitives, primitiveKey{Primitive: gli.searchPanel, Key: keyBindings.KeyID("search")})
	gli.focusPrimitives = append(gli.focusPrimitives, primitiveKey{Primitive: gli.keyItemsPanel, Key: keyBindings.KeyID("keys")})

	gli.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {

		if gli.config.Debug {
			gli.outputChan <- OutputMessage{Message: fmt.Sprintf("Key %s pressed", tcell.KeyNames[event.Key()])}
		}

		name := keyBindings.SearchKey(event.Key())
		switch name {
		case "switch_focus":
			nextFocusIndex := gli.currentFocusIndex + 1
			if nextFocusIndex > len(gli.focusPrimitives)-1 {
				nextFocusIndex = 0
			}

			gli.app.SetFocus(gli.focusPrimitives[nextFocusIndex].Primitive)
			gli.currentFocusIndex = nextFocusIndex

			return nil
		case "quit":
			gli.app.Stop()
		case "command":
			if gli.commandMode {
				gli.commandMode = false
				gli.redrawRightPanel(gli.mainPanel)
				gli.app.SetFocus(gli.searchPanel)
				gli.currentFocusIndex = 0
			} else {
				gli.commandMode = true
				gli.redrawRightPanel(gli.commandPanel)

				for i, p := range gli.focusPrimitives {
					if p.Primitive == gli.commandFormPanel {
						gli.app.SetFocus(gli.commandFormPanel)
						gli.currentFocusIndex = i
					}
				}
			}
		default:
			for i, pv := range gli.focusPrimitives {
				if pv.Key == name {
					gli.app.SetFocus(pv.Primitive)
					gli.currentFocusIndex = i
					break
				}
			}
		}

		return event
	})

	return gli
}

// Start create the ui and start the program
func (gli *RedisGli) Start() error {
	go gli.app.QueueUpdateDraw(func() {
		info, err := RedisServerInfo(gli.config, gli.redisClient)
		if err != nil {
			gli.outputChan <- OutputMessage{tcell.ColorRed, fmt.Sprintf("errors: %s", err)}
		}

		gli.helpServerInfoPanel.SetText(info)

		keys, _, err := gli.redisClient.Scan(0, "*", gli.maxKeyLimit).Result()
		if err != nil {
			gli.outputChan <- OutputMessage{tcell.ColorRed, fmt.Sprintf("errors: %s", err)}
			return
		}

		gli.summaryPanel.SetText(fmt.Sprintf(" Total matched: %d", len(keys)))

		for i, k := range keys {
			gli.keyItemsPanel.AddItem(gli.keyItemsFormat(i, k), "", 0, gli.itemSelectedHandler(i, k))
		}

		gli.app.SetFocus(gli.keyItemsPanel)
	})

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case out := <-gli.outputChan:
				(func(out OutputMessage) {
					gli.app.QueueUpdateDraw(func() {
						// gli.outputPanel.SetTextColor(out.Color).SetText(fmt.Sprintf("[%s] %s", time.Now().Format(time.RFC3339), out.Message))
						gli.outputPanel.AddItem(fmt.Sprintf("[%s] %s", time.Now().Format(time.RFC3339), out.Message), "", 0, nil)
						gli.outputPanel.SetCurrentItem(-1)
					})
				})(out)
			case <-ticker.C:
				gli.app.QueueUpdateDraw(func() {
					info, err := RedisServerInfo(gli.config, gli.redisClient)
					if err != nil {
						gli.outputChan <- OutputMessage{tcell.ColorRed, fmt.Sprintf("errors: %s", err)}
					}

					gli.helpServerInfoPanel.SetText(info)
				})
			}
		}
	}()

	gli.pages = tview.NewPages()
	gli.pages.AddPage("base", gli.layout, true, true)

	return gli.app.SetRoot(gli.pages, true).Run()
}

func (gli *RedisGli) redrawRightPanel(center tview.Primitive) {
	gli.rightPanel.RemoveItem(gli.metaPanel).
		RemoveItem(gli.outputPanel).
		RemoveItem(gli.mainPanel).
		RemoveItem(gli.commandPanel).
		RemoveItem(gli.helpPanel)

	gli.rightPanel.AddItem(gli.helpPanel, 5, 1, false).
		AddItem(gli.metaPanel, 4, 1, false).
		AddItem(center, 0, 7, false).
		AddItem(gli.outputPanel, 8, 1, false)
}

func (gli *RedisGli) createSummaryPanel() *tview.TextView {
	panel := tview.NewTextView()
	panel.SetBorder(true).SetTitle(" Info ")
	return panel
}

func (gli *RedisGli) keyItemsFormat(index int, key string) string {
	return fmt.Sprintf("%3d | %s", index+1, key)
}

func (gli *RedisGli) createCommandPanel() *tview.Flex {
	flex := tview.NewFlex().SetDirection(tview.FlexRow)

	resultPanel := tview.NewTextView()
	resultPanel.SetBorder(true).SetTitle(fmt.Sprintf(" Results (%s) ", keyBindings.Name("command_result")))

	formPanel := tview.NewInputField().SetLabel("Command ")
	var locked bool
	formPanel.SetDoneFunc(func(key tcell.Key) {
		if key != tcell.KeyEnter {
			return
		}

		if locked {
			pageID := "alert"
			gli.pages.AddPage(
				pageID,
				tview.NewModal().
					SetText("Other command is processing, please wait...").
					AddButtons([]string{"OK"}).
					SetDoneFunc(func(buttonIndex int, buttonLabel string) {
						gli.pages.HidePage(pageID).RemovePage(pageID)
						gli.app.SetFocus(formPanel)
					}),
				false,
				true,
			)

			return
		}

		cmdText := formPanel.GetText()
		gli.outputChan <- OutputMessage{Color: tcell.ColorOrange, Message: fmt.Sprintf("Command %s is processing...", cmdText)}
		locked = true

		go func(cmdText string) {
			gli.app.QueueUpdateDraw(func() {
				defer func() {
					locked = false
				}()

				res, err := RedisExecute(gli.redisClient, cmdText)
				if err != nil {
					gli.outputChan <- OutputMessage{tcell.ColorRed, fmt.Sprintf("errors: %s", err)}
					return
				}

				resultPanel.SetText(fmt.Sprintf("%v", res))
				gli.outputChan <- OutputMessage{tcell.ColorGreen, fmt.Sprintf("Command %s succeed", cmdText)}
			})
		}(cmdText)

		formPanel.SetText("")
	})
	// formPanel.SetBackgroundColor(tcell.ColorOrange)
	formPanel.SetBorder(true).SetTitle(fmt.Sprintf(" Commands (%s) ", keyBindings.Name("command_focus")))

	gli.commandFormPanel = formPanel
	gli.commandResultPanel = resultPanel

	gli.focusPrimitives = append(gli.focusPrimitives, primitiveKey{Primitive: gli.commandFormPanel, Key: keyBindings.KeyID("command_focus")})
	gli.focusPrimitives = append(gli.focusPrimitives, primitiveKey{Primitive: gli.commandResultPanel, Key: keyBindings.KeyID("command_result")})

	flex.AddItem(formPanel, 4, 0, false).
		AddItem(resultPanel, 0, 1, false)

	return flex
}

// createSearchPanel create search panel
func (gli *RedisGli) createSearchPanel() *tview.InputField {
	searchArea := tview.NewInputField().SetLabel(" KeyID ")
	searchArea.SetDoneFunc(func(key tcell.Key) {
		if key != tcell.KeyEnter {
			return
		}
		var text = searchArea.GetText()

		var keys []string
		var err error

		if text == "" || text == "*" {
			keys, _, err = gli.redisClient.Scan(0, text, gli.maxKeyLimit).Result()
		} else {
			keys, err = gli.redisClient.Keys(text).Result()
		}

		if err != nil {
			gli.outputChan <- OutputMessage{tcell.ColorRed, fmt.Sprintf("errors: %s", err)}
			return
		}

		gli.keyItemsPanel.Clear()

		gli.summaryPanel.SetText(fmt.Sprintf(" Total matched: %d", len(keys)))
		for i, k := range keys {
			gli.keyItemsPanel.AddItem(gli.keyItemsFormat(i, k), "", 0, gli.itemSelectedHandler(i, k))
		}
	})
	searchArea.SetBorder(true).SetTitle(fmt.Sprintf(" Search (%s) ", keyBindings.Name("search")))
	return searchArea
}

// createKeyItemsPanel create key items panel
func (gli *RedisGli) createKeyItemsPanel() *tview.List {
	keyItemsList := tview.NewList().ShowSecondaryText(false)
	keyItemsList.SetBorder(true).SetTitle(fmt.Sprintf(" Keys (%s) ", keyBindings.Name("keys")))
	return keyItemsList
}

// primitivesFilter is a filter for primitives
func (gli *RedisGli) primitivesFilter(items []primitiveKey, filter func(item primitiveKey) bool) []primitiveKey {
	res := make([]primitiveKey, 0)
	for _, item := range items {
		if filter(item) {
			res = append(res, item)
		}
	}

	return res
}

// createMetaPanel create a panel for meta info
func (gli *RedisGli) createMetaPanel() *tview.TextView {
	metaInfoArea := tview.NewTextView().SetDynamicColors(true).SetRegions(true)
	metaInfoArea.SetBorder(true).SetTitle(" Meta ")

	return metaInfoArea
}

// createMainPanel create main panel
func (gli *RedisGli) createMainPanel() *tview.Flex {
	mainArea := tview.NewFlex()
	mainArea.SetBorder(true).SetTitle(" Value ")

	mainArea.AddItem(gli.welcomeScreen, 0, 1, false)

	return mainArea
}

// createOutputPanel create a panel for outputFunc
func (gli *RedisGli) createOutputPanel() *tview.List {
	outputArea := tview.NewList().ShowSecondaryText(false)
	outputArea.SetBorder(true).SetTitle(fmt.Sprintf(" Output (%s) ", keyBindings.Name("output")))

	gli.focusPrimitives = append(gli.focusPrimitives, primitiveKey{Primitive: outputArea, Key: keyBindings.KeyID("output")})

	return outputArea
}

// createHelpPanel create a panel for help message display
func (gli *RedisGli) createHelpPanel() *tview.Flex {
	helpPanel := tview.NewFlex().SetDirection(tview.FlexRow)
	helpPanel.SetBorder(true).SetTitle(fmt.Sprintf(" Version: %s (%s) ", gli.version, gli.gitCommit))

	gli.helpServerInfoPanel = tview.NewTextView().SetDynamicColors(true).SetRegions(true)
	helpPanel.AddItem(gli.helpServerInfoPanel, 2, 1, false)

	gli.helpMessagePanel = tview.NewTextView()
	gli.helpMessagePanel.SetTextColor(tcell.ColorOrange).SetText(fmt.Sprintf(
		" ❈ %s - open command panel, %s - switch focus, %s - quit",
		keyBindings.Name("command"),
		keyBindings.Name("switch_focus"),
		keyBindings.Name("quit"),
	))

	helpPanel.AddItem(gli.helpMessagePanel, 1, 1, false)

	return helpPanel
}

// createKeySelectedHandler create a handler for item selected event
func (gli *RedisGli) createKeySelectedHandler() func(index int, key string) func() {

	// 用于KV展示的视图
	mainStringView := tview.NewTextView()
	mainStringView.SetBorder(true).SetTitle(fmt.Sprintf(" Value (%s) ", keyBindings.Name("key_string_value")))

	mainHashView := tview.NewList().ShowSecondaryText(false)
	mainHashView.SetBorder(true).SetTitle(fmt.Sprintf(" Hash KeyID (%s) ", keyBindings.Name("key_hash")))

	mainListView := tview.NewList().ShowSecondaryText(false).SetSecondaryTextColor(tcell.ColorOrangeRed)
	mainListView.SetBorder(true).SetTitle(fmt.Sprintf(" Value (%s) ", keyBindings.Name("key_list_value")))

	return func(index int, key string) func() {
		return func() {
			keyType, err := gli.redisClient.Type(key).Result()
			if err != nil {
				gli.outputChan <- OutputMessage{tcell.ColorRed, fmt.Sprintf("errors: %s", err)}
				return
			}

			ttl, err := gli.redisClient.TTL(key).Result()
			if err != nil {
				gli.outputChan <- OutputMessage{tcell.ColorRed, fmt.Sprintf("errors: %s", err)}
				return
			}

			// 移除主区域的边框，因为展示区域已经带有边框了
			gli.mainPanel.RemoveItem(gli.welcomeScreen).SetBorder(false)

			// 重置展示视图
			mainHashView.Clear()
			mainStringView.Clear()
			mainListView.Clear().ShowSecondaryText(false)

			gli.focusPrimitives = gli.primitivesFilter(gli.focusPrimitives, func(item primitiveKey) bool {
				return item.Primitive != mainHashView && item.Primitive != mainListView && item.Primitive != mainStringView
			})

			gli.mainPanel.RemoveItem(mainStringView)
			gli.mainPanel.RemoveItem(mainHashView)
			gli.mainPanel.RemoveItem(mainListView)

			// 根据不同的kv类型，展示不同的视图
			switch keyType {
			case "string":
				result, err := gli.redisClient.Get(key).Result()
				if err != nil {
					gli.outputChan <- OutputMessage{tcell.ColorRed, fmt.Sprintf("errors: %s", err)}
					return
				}

				gli.mainPanel.AddItem(mainStringView.SetText(fmt.Sprintf(" %s", result)), 0, 1, false)
				gli.focusPrimitives = append(gli.focusPrimitives, primitiveKey{Primitive: mainStringView, Key: keyBindings.KeyID("key_string_value")})
			case "list":
				values, err := gli.redisClient.LRange(key, 0, 1000).Result()
				if err != nil {
					gli.outputChan <- OutputMessage{tcell.ColorRed, fmt.Sprintf("errors: %s", err)}
					return
				}

				for i, v := range values {
					mainListView.AddItem(fmt.Sprintf(" %3d | %s", i+1, v), "", 0, nil)
				}

				gli.mainPanel.AddItem(mainListView, 0, 1, false)
				gli.focusPrimitives = append(gli.focusPrimitives, primitiveKey{Primitive: mainListView, Key: keyBindings.KeyID("key_list_value")})

			case "set":
				values, err := gli.redisClient.SMembers(key).Result()
				if err != nil {
					gli.outputChan <- OutputMessage{tcell.ColorRed, fmt.Sprintf("errors: %s", err)}
					return
				}

				for i, v := range values {
					mainListView.AddItem(fmt.Sprintf(" %3d | %s", i+1, v), "", 0, nil)
				}

				gli.mainPanel.AddItem(mainListView, 0, 1, false)
				gli.focusPrimitives = append(gli.focusPrimitives, primitiveKey{Primitive: mainListView, Key: keyBindings.KeyID("key_list_value")})

			case "zset":
				values, err := gli.redisClient.ZRangeWithScores(key, 0, 1000).Result()
				if err != nil {
					gli.outputChan <- OutputMessage{tcell.ColorRed, fmt.Sprintf("errors: %s", err)}
					return
				}

				mainListView.ShowSecondaryText(true)
				for i, z := range values {
					val := fmt.Sprintf(" %3d | %v", i+1, z.Member)
					score := fmt.Sprintf("    Score: %v", z.Score)

					mainListView.AddItem(val, score, 0, nil)
				}

				gli.mainPanel.AddItem(mainListView, 0, 1, false)
				gli.focusPrimitives = append(gli.focusPrimitives, primitiveKey{Primitive: mainListView, Key: keyBindings.KeyID("key_list_value")})

			case "hash":
				hashKeys, err := gli.redisClient.HKeys(key).Result()
				if err != nil {
					gli.outputChan <- OutputMessage{tcell.ColorRed, fmt.Sprintf("errors: %s", err)}
					return
				}

				for i, k := range hashKeys {
					mainHashView.AddItem(fmt.Sprintf(" %3d | %s", i+1, k), "", 0, (func(k string) func() {
						return func() {
							val, err := gli.redisClient.HGet(key, k).Result()
							if err != nil {
								gli.outputChan <- OutputMessage{tcell.ColorRed, fmt.Sprintf("errors: %s", err)}
								return
							}

							mainStringView.SetText(fmt.Sprintf(" %s", val)).SetTitle(fmt.Sprintf(" Value: %s ", k))
						}
					})(k))
				}

				gli.mainPanel.AddItem(mainHashView, 0, 3, false).
					AddItem(mainStringView, 0, 7, false)

				gli.focusPrimitives = append(gli.focusPrimitives, primitiveKey{Primitive: mainHashView, Key: keyBindings.KeyID("key_hash")})
				gli.focusPrimitives = append(gli.focusPrimitives, primitiveKey{Primitive: mainStringView, Key: keyBindings.KeyID("key_string_value")})
			}
			gli.outputChan <- OutputMessage{tcell.ColorGreen, fmt.Sprintf("query %s OK, type=%s, ttl=%s", key, keyType, ttl.String())}
			gli.metaPanel.SetText(fmt.Sprintf("KeyID: %s\nType: %s, TTL: %s", key, keyType, ttl.String())).SetTextAlign(tview.AlignCenter)
		}
	}
}
