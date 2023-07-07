package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"start-feishubot/services/openai"
)

func setDefaultPrompt(msg []openai.Messages) []openai.Messages {
	if !hasSystemRole(msg) {
		msg = append(msg, openai.Messages{
			Role: "system", Content: "You are ChatGPT, " +
				"a large language model trained by OpenAI. " +
				"Answer in user's language as concisely as" +
				" possible. Knowledge cutoff: 20230601 " +
				"Current date" + time.Now().Format("20060102"),
		})
	}
	return msg
}

type MessageAction struct { /*消息*/
}

func (*MessageAction) Execute(a *ActionInfo) bool {
	if a.handler.config.StreamMode {
		return true
	}
	msg := a.handler.sessionCache.GetMsg(*a.info.sessionId)
	// 如果没有提示词，默认模拟ChatGPT
	msg = setDefaultPrompt(msg)
	msg = append(msg, openai.Messages{
		Role: "user", Content: a.info.qParsed,
	})

	//fmt.Println("msg", msg)
	//logger.Debug("msg", msg)
	// get ai mode as temperature
	aiMode := a.handler.sessionCache.GetAIMode(*a.info.sessionId)
	completions, err := a.handler.gpt.Completions(msg, aiMode)
	if err != nil {
		replyMsg(*a.ctx, fmt.Sprintf(
			"🤖️：消息机器人摆烂了，请稍后再试～\n错误信息: %v", err), a.info.msgId)
		return false
	}
	msg = append(msg, completions)
	a.handler.sessionCache.SetMsg(*a.info.sessionId, msg)
	//if new topic
	if len(msg) == 3 {
		//fmt.Println("new topic", msg[1].Content)
		sendNewTopicCard(*a.ctx, a.info.sessionId, a.info.msgId,
			completions.Content)
		return false
	}
	err = replyMsg(*a.ctx, completions.Content, a.info.msgId)
	if err != nil {
		replyMsg(*a.ctx, fmt.Sprintf(
			"🤖️：消息机器人摆烂了，请稍后再试～\n错误信息: %v", err), a.info.msgId)
		return false
	}
	return true
}

//判断msg中的是否包含system role
func hasSystemRole(msg []openai.Messages) bool {
	for _, m := range msg {
		if m.Role == "system" {
			return true
		}
	}
	return false
}

type StreamMessageAction struct { /*消息*/
}

func (m *StreamMessageAction) Execute(a *ActionInfo) bool {
	if !a.handler.config.StreamMode {
		return true
	}
	msg := a.handler.sessionCache.GetMsg(*a.info.sessionId)
	// 如果没有提示词，默认模拟ChatGPT
	msg = setDefaultPrompt(msg)
	msg = append(msg, openai.Messages{
		Role: "user", Content: a.info.qParsed,
	})

	cardId, err2 := sendOnProcess(a)
	if err2 != nil {
		return false
	}

	answer := ""
	chatResponseStream := make(chan string)
	done := make(chan struct{}) // 添加 done 信号，保证 goroutine 正确退出
	noContentTimeout := time.AfterFunc(10*time.Second, func() {
		log.Println("no content timeout")
		close(done)
		err := updateFinalCard(*a.ctx, "请求超时", cardId)
		if err != nil {
			return
		}
		return
	})
	defer noContentTimeout.Stop()

	go func() {
		defer func() {
			if err := recover(); err != nil {
				err := updateFinalCard(*a.ctx, "聊天失败", cardId)
				if err != nil {
					return
				}
			}
		}()

		//log.Printf("UserId: %s , Request: %s", a.info.userId, msg)
		aiMode := a.handler.sessionCache.GetAIMode(*a.info.sessionId)
		if err := a.handler.gpt.StreamChat(*a.ctx, msg, aiMode,
			chatResponseStream); err != nil {
			err := updateFinalCard(*a.ctx, "聊天失败", cardId)
			if err != nil {
				return
			}
			close(done) // 关闭 done 信号
		}

		close(done) // 关闭 done 信号
	}()
	ticker := time.NewTicker(700 * time.Millisecond)
	defer ticker.Stop() // 注意在函数结束时停止 ticker
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				err := updateTextCard(*a.ctx, answer, cardId)
				if err != nil {
					return
				}
			}
		}
	}()
	for {
		select {
		case res, ok := <-chatResponseStream:
			if !ok {
				return false
			}
			noContentTimeout.Stop()
			answer += res
			//pp.Println("answer", answer)
		case <-done: // 添加 done 信号的处理
			err := updateFinalCard(*a.ctx, answer, cardId)
			if err != nil {
				return false
			}
			ticker.Stop()
			msg := append(msg, openai.Messages{
				Role: "assistant", Content: answer,
			})
			a.handler.sessionCache.SetMsg(*a.info.sessionId, msg)
			close(chatResponseStream)
			log.Printf("\n\n\n")
			jsonByteArray, err := json.Marshal(msg)
			if err != nil {
				log.Println(err)
			}
			jsonStr := strings.ReplaceAll(string(jsonByteArray), "\\n", "")
			jsonStr = strings.ReplaceAll(jsonStr, "\n", "")
			log.Printf("\n\n\n")
			return false
		}
	}
}

func sendOnProcess(a *ActionInfo) (*string, error) {
	// send 正在处理中
	cardId, err := sendOnProcessCard(*a.ctx, a.info.sessionId, a.info.msgId)
	if err != nil {
		return nil, err
	}
	return cardId, nil

}
