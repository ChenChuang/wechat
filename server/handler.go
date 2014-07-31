// @description wechat 是腾讯微信公众平台 api 的 golang 语言封装
// @link        https://github.com/chanxuehong/wechat for the canonical source repository
// @license     https://github.com/chanxuehong/wechat/blob/master/LICENSE
// @authors     chanxuehong(chanxuehong@gmail.com)

package server

import (
	"encoding/xml"
	"errors"
	"github.com/chanxuehong/wechat/message/request"
	"io"
	"net/http"
	"net/url"
	"sync"
)

// 对于公众号开发模式, 都会要求提供一个 URL 来处理微信服务器推送过来的消息和事件,
// Handler 就是处理推送到这个 URL 上的消息(事件).
//  Handler 实现了 http.Handler 接口, 使用时把 Handler 绑定到 URL 的 path 上即可;
//  Handler 并发安全.
type Handler struct {
	setting HandlerSetting

	// 对于微信服务器推送过来的请求, 处理过程中有些中间状态比较大的变量, 所以可以缓存起来.
	//  NOTE: require go1.3+ , 如果你的环境不满足这个条件, 可以自己实现一个简单的 Pool,
	//        see github.com/chanxuehong/util/pool
	bufferUnitPool sync.Pool
}

func NewHandler(setting *HandlerSetting) (handler *Handler) {
	if setting == nil {
		panic("setting == nil")
	}

	handler = &Handler{
		bufferUnitPool: sync.Pool{New: newBufferUnit},
	}
	handler.setting.initialize(setting)

	return
}

// Handler 实现 http.Handler 接口
func (handler *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "POST": // 处理从微信服务器推送过来的消息(事件) ==============================
		var urlValues url.Values
		var signature, timestamp, nonce string
		var err error

		if r.URL == nil {
			handler.setting.InvalidRequestHandler(w, r, errors.New("r.URL == nil"))
			return
		}
		if urlValues, err = url.ParseQuery(r.URL.RawQuery); err != nil {
			handler.setting.InvalidRequestHandler(w, r, err)
			return
		}

		if signature = urlValues.Get("signature"); signature == "" {
			handler.setting.InvalidRequestHandler(w, r, errors.New("signature is empty"))
			return
		}
		if timestamp = urlValues.Get("timestamp"); timestamp == "" {
			handler.setting.InvalidRequestHandler(w, r, errors.New("timestamp is empty"))
			return
		}
		if nonce = urlValues.Get("nonce"); nonce == "" {
			handler.setting.InvalidRequestHandler(w, r, errors.New("nonce is empty"))
			return
		}

		bufferUnit := handler.getBufferUnitFromPool() // *bufferUnit
		defer handler.putBufferUnitToPool(bufferUnit) // important!

		if !checkSignature(signature, timestamp, nonce, handler.setting.Token, bufferUnit.signatureBuf[:]) {
			handler.setting.InvalidRequestHandler(w, r, errors.New("check signature failed"))
			return
		}

		if _, err = io.Copy(bufferUnit.msgBuf, r.Body); err != nil {
			handler.setting.InvalidRequestHandler(w, r, err)
			return
		}

		msgReqBody := bufferUnit.msgBuf.Bytes()
		msgReq := &bufferUnit.msgRequest // & 不能丢
		if err = xml.Unmarshal(msgReqBody, msgReq); err != nil {
			handler.setting.InvalidRequestHandler(w, r, err)
			return
		}

		// request router, 可一个根据自己的实际业务调整顺序!
		switch msgReq.MsgType {
		case request.MSG_TYPE_TEXT:
			text := request.Text{
				CommonHead: msgReq.CommonHead,
				MsgId:      msgReq.MsgId,
				Content:    msgReq.Content,
			}
			handler.setting.TextRequestHandler(w, r, &text)

		case request.MSG_TYPE_EVENT:
			// event router
			switch msgReq.Event {
			case request.EVENT_TYPE_CLICK:
				event := request.MenuClickEvent{
					CommonHead: msgReq.CommonHead,
					Event:      msgReq.Event,
					EventKey:   msgReq.EventKey,
				}
				handler.setting.MenuClickEventHandler(w, r, &event)

			case request.EVENT_TYPE_VIEW:
				event := request.MenuViewEvent{
					CommonHead: msgReq.CommonHead,
					Event:      msgReq.Event,
					EventKey:   msgReq.EventKey,
				}
				handler.setting.MenuViewEventHandler(w, r, &event)

			case request.EVENT_TYPE_LOCATION:
				event := request.LocationEvent{
					CommonHead: msgReq.CommonHead,
					Event:      msgReq.Event,
					Latitude:   msgReq.Latitude,
					Longitude:  msgReq.Longitude,
					Precision:  msgReq.Precision,
				}
				handler.setting.LocationEventHandler(w, r, &event)

			case request.EVENT_TYPE_MERCHANTORDER:
				event := request.MerchantOrderEvent{
					CommonHead:  msgReq.CommonHead,
					Event:       msgReq.Event,
					OrderId:     msgReq.OrderId,
					OrderStatus: msgReq.OrderStatus,
					ProductId:   msgReq.ProductId,
					SkuInfo:     msgReq.SkuInfo,
				}
				handler.setting.MerchantOrderEventHandler(w, r, &event)

			case request.EVENT_TYPE_SUBSCRIBE:
				if msgReq.Ticket == "" { // 普通订阅
					event := request.SubscribeEvent{
						CommonHead: msgReq.CommonHead,
						Event:      msgReq.Event,
					}
					handler.setting.SubscribeEventHandler(w, r, &event)

				} else { // 扫描二维码订阅
					event := request.SubscribeByScanEvent{
						CommonHead: msgReq.CommonHead,
						Event:      msgReq.Event,
						EventKey:   msgReq.EventKey,
						Ticket:     msgReq.Ticket,
					}
					handler.setting.SubscribeByScanEventHandler(w, r, &event)
				}

			case request.EVENT_TYPE_UNSUBSCRIBE:
				event := request.UnsubscribeEvent{
					CommonHead: msgReq.CommonHead,
					Event:      msgReq.Event,
				}
				handler.setting.UnsubscribeEventHandler(w, r, &event)

			case request.EVENT_TYPE_SCAN:
				event := request.ScanEvent{
					CommonHead: msgReq.CommonHead,
					Event:      msgReq.Event,
					EventKey:   msgReq.EventKey,
					Ticket:     msgReq.Ticket,
				}
				handler.setting.ScanEventHandler(w, r, &event)

			case request.EVENT_TYPE_MASSSENDJOBFINISH:
				event := request.MassSendJobFinishEvent{
					CommonHead:  msgReq.CommonHead,
					Event:       msgReq.Event,
					MsgId:       msgReq.MsgID, // NOTE
					Status:      msgReq.Status,
					TotalCount:  msgReq.TotalCount,
					FilterCount: msgReq.FilterCount,
					SentCount:   msgReq.SentCount,
					ErrorCount:  msgReq.ErrorCount,
				}
				handler.setting.MassSendJobFinishEventHandler(w, r, &event)

			default: // unknown event
				// 因为 msgReqBody 底层需要缓存, 所以这里需要一个副本
				msgReqBodyCopy := make([]byte, len(msgReqBody))
				copy(msgReqBodyCopy, msgReqBody)
				handler.setting.UnknownRequestHandler(w, r, msgReqBodyCopy)
			}

		case request.MSG_TYPE_LINK:
			link := request.Link{
				CommonHead:  msgReq.CommonHead,
				MsgId:       msgReq.MsgId,
				Title:       msgReq.Title,
				Description: msgReq.Description,
				URL:         msgReq.URL,
			}
			handler.setting.LinkRequestHandler(w, r, &link)

		case request.MSG_TYPE_VOICE:
			voice := request.Voice{
				CommonHead:  msgReq.CommonHead,
				MsgId:       msgReq.MsgId,
				MediaId:     msgReq.MediaId,
				Format:      msgReq.Format,
				Recognition: msgReq.Recognition,
			}
			handler.setting.VoiceRequestHandler(w, r, &voice)

		case request.MSG_TYPE_LOCATION:
			location := request.Location{
				CommonHead: msgReq.CommonHead,
				MsgId:      msgReq.MsgId,
				LocationX:  msgReq.LocationX,
				LocationY:  msgReq.LocationY,
				Scale:      msgReq.Scale,
				Label:      msgReq.Label,
			}
			handler.setting.LocationRequestHandler(w, r, &location)

		case request.MSG_TYPE_IMAGE:
			image := request.Image{
				CommonHead: msgReq.CommonHead,
				MsgId:      msgReq.MsgId,
				MediaId:    msgReq.MediaId,
				PicURL:     msgReq.PicURL,
			}
			handler.setting.ImageRequestHandler(w, r, &image)

		case request.MSG_TYPE_VIDEO:
			video := request.Video{
				CommonHead:   msgReq.CommonHead,
				MsgId:        msgReq.MsgId,
				MediaId:      msgReq.MediaId,
				ThumbMediaId: msgReq.ThumbMediaId,
			}
			handler.setting.VideoRequestHandler(w, r, &video)

		default: // unknown request message type
			// 因为 msgReqBody 底层需要缓存, 所以这里需要一个副本
			msgReqBodyCopy := make([]byte, len(msgReqBody))
			copy(msgReqBodyCopy, msgReqBody)
			handler.setting.UnknownRequestHandler(w, r, msgReqBodyCopy)
		}

	case "GET": // 首次验证 ======================================================
		var urlValues url.Values
		var signature, timestamp, nonce, echostr string
		var err error

		if r.URL == nil {
			handler.setting.InvalidRequestHandler(w, r, errors.New("r.URL == nil"))
			return
		}
		if urlValues, err = url.ParseQuery(r.URL.RawQuery); err != nil {
			handler.setting.InvalidRequestHandler(w, r, err)
			return
		}

		if signature = urlValues.Get("signature"); signature == "" {
			handler.setting.InvalidRequestHandler(w, r, errors.New("signature is empty"))
			return
		}
		if timestamp = urlValues.Get("timestamp"); timestamp == "" {
			handler.setting.InvalidRequestHandler(w, r, errors.New("timestamp is empty"))
			return
		}
		if nonce = urlValues.Get("nonce"); nonce == "" {
			handler.setting.InvalidRequestHandler(w, r, errors.New("nonce is empty"))
			return
		}
		if echostr = urlValues.Get("echostr"); echostr == "" {
			handler.setting.InvalidRequestHandler(w, r, errors.New("echostr is empty"))
			return
		}

		bufferUnit := handler.getBufferUnitFromPool() // *bufferUnit
		defer handler.putBufferUnitToPool(bufferUnit) // important!

		if !checkSignature(signature, timestamp, nonce, handler.setting.Token, bufferUnit.signatureBuf[:]) {
			handler.setting.InvalidRequestHandler(w, r, errors.New("check signature failed"))
			return
		}

		io.WriteString(w, echostr)
	}
}