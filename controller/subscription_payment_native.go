package controller

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/payment/alipay"
	"github.com/QuantumNous/new-api/payment/wechat"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// ==================== 微信支付订阅 ====================

// SubscriptionWeChatPayRequest 微信支付订阅请求
type SubscriptionWeChatPayRequest struct {
	PlanId   int    `json:"plan_id"`
	Scene    string `json:"scene"`     // native/h5/jsapi
	OpenID   string `json:"open_id"`   // JSAPI必填
	ClientIP string `json:"client_ip"` // H5必填
}

// SubscriptionRequestWeChatPay 微信支付购买订阅
func SubscriptionRequestWeChatPay(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	var req SubscriptionWeChatPayRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}

	plan, err := model.GetSubscriptionPlanById(req.PlanId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !plan.Enabled {
		common.ApiErrorMsg(c, "套餐未启用")
		return
	}
	if plan.PriceAmount < 0.01 {
		common.ApiErrorMsg(c, "套餐金额过低")
		return
	}

	userId := c.GetInt("id")
	if plan.MaxPurchasePerUser > 0 {
		count, err := model.CountUserSubscriptionsByPlan(userId, plan.Id)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if count >= int64(plan.MaxPurchasePerUser) {
			common.ApiErrorMsg(c, "已达到该套餐购买上限")
			return
		}
	}

	// 确定支付场景
	var scene wechat.PayScene
	switch req.Scene {
	case "h5":
		scene = wechat.SceneH5
	case "jsapi":
		scene = wechat.SceneJSAPI
	default:
		scene = wechat.SceneNative
	}

	tradeNo := fmt.Sprintf("%s%d", common.GetRandomString(6), time.Now().Unix())
	tradeNo = fmt.Sprintf("SUBUSR%dNO%s", userId, tradeNo)

	client := GetWeChatPayClient()
	if client == nil {
		common.ApiErrorMsg(c, "微信支付未配置")
		return
	}

	// 创建订阅订单
	order := &model.SubscriptionOrder{
		UserId:          userId,
		PlanId:          plan.Id,
		Money:           plan.PriceAmount,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodWeChatPay,
		PaymentProvider: model.PaymentProviderWeChatPay,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := order.Insert(); err != nil {
		common.ApiErrorMsg(c, "创建订单失败")
		return
	}

	// 金额转换为分
	amountInFen := int(plan.PriceAmount * 100)

	orderResp, err := client.CreateOrder(&wechat.CreateOrderRequest{
		Scene:       scene,
		OutTradeNo:  tradeNo,
		Description: fmt.Sprintf("SUB:%s", plan.Title),
		Amount:      int64(amountInFen),
		OpenID:      req.OpenID,
		ClientIP:    req.ClientIP,
	})
	if err != nil {
		_ = model.ExpireSubscriptionOrder(tradeNo, model.PaymentProviderWeChatPay)
		common.ApiErrorMsg(c, "拉起支付失败")
		return
	}

	data := gin.H{"trade_no": tradeNo}
	switch scene {
	case wechat.SceneNative:
		data["code_url"] = orderResp.CodeURL
		data["type"] = "native"
	case wechat.SceneH5:
		data["h5_url"] = orderResp.H5URL
		data["type"] = "h5"
	case wechat.SceneJSAPI:
		payParams, err := client.GetJSAPIPayParams(orderResp.PrepayID)
		if err != nil {
			common.ApiErrorMsg(c, "获取支付参数失败")
			return
		}
		data["jsapi_params"] = payParams
		data["type"] = "jsapi"
	}

	c.JSON(http.StatusOK, gin.H{"message": "success", "data": data})
}

// SubscriptionWeChatPayNotify 微信支付订阅回调
func SubscriptionWeChatPayNotify(c *gin.Context) {
	// 复用充值回调的验签逻辑，但完成订阅订单
	if !isWeChatPayWebhookEnabled() {
		c.JSON(http.StatusForbidden, gin.H{"code": "FAIL", "message": "webhook disabled"})
		return
	}

	body, err := readBody(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "FAIL", "message": "read body failed"})
		return
	}

	headers := extractWeChatPayHeaders(c)
	client := GetWeChatPayClient()
	if client == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "FAIL", "message": "client not initialized"})
		return
	}

	resource, err := client.VerifyAndDecryptNotify(body, headers)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "FAIL", "message": "verification failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": "SUCCESS", "message": "成功"})

	if resource.TradeState == "SUCCESS" {
		LockOrder(resource.OutTradeNo)
		defer UnlockOrder(resource.OutTradeNo)

		if err := model.CompleteSubscriptionOrder(resource.OutTradeNo, fmt.Sprintf(`{"transaction_id":"%s"}`, resource.TransactionID), model.PaymentProviderWeChatPay, model.PaymentMethodWeChatPay); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 订阅订单完成失败 trade_no=%s error=%q", resource.OutTradeNo, err.Error()))
		}
	}
}

// ==================== 支付宝订阅 ====================

// SubscriptionAlipayRequest 支付宝订阅请求
type SubscriptionAlipayRequest struct {
	PlanId    int    `json:"plan_id"`
	Scene     string `json:"scene"`      // page/wap/jsapi
	ReturnURL string `json:"return_url"` // 支付完成后跳转URL
	AuthToken string `json:"auth_token"` // JSAPI必填
}

// SubscriptionRequestAlipay 支付宝购买订阅
func SubscriptionRequestAlipay(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	var req SubscriptionAlipayRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}

	plan, err := model.GetSubscriptionPlanById(req.PlanId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !plan.Enabled {
		common.ApiErrorMsg(c, "套餐未启用")
		return
	}
	if plan.PriceAmount < 0.01 {
		common.ApiErrorMsg(c, "套餐金额过低")
		return
	}

	userId := c.GetInt("id")
	if plan.MaxPurchasePerUser > 0 {
		count, err := model.CountUserSubscriptionsByPlan(userId, plan.Id)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if count >= int64(plan.MaxPurchasePerUser) {
			common.ApiErrorMsg(c, "已达到该套餐购买上限")
			return
		}
	}

	var scene alipay.PayScene
	switch req.Scene {
	case "wap":
		scene = alipay.SceneWap
	case "jsapi":
		scene = alipay.SceneJSAPI
	default:
		scene = alipay.ScenePage
	}

	tradeNo := fmt.Sprintf("%s%d", common.GetRandomString(6), time.Now().Unix())
	tradeNo = fmt.Sprintf("SUBUSR%dNO%s", userId, tradeNo)

	client := GetAlipayClient()
	if client == nil {
		common.ApiErrorMsg(c, "支付宝未配置")
		return
	}

	order := &model.SubscriptionOrder{
		UserId:          userId,
		PlanId:          plan.Id,
		Money:           plan.PriceAmount,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodAlipay,
		PaymentProvider: model.PaymentProviderAlipay,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := order.Insert(); err != nil {
		common.ApiErrorMsg(c, "创建订单失败")
		return
	}

	callBackAddress := service.GetCallbackAddress()
	returnURL := req.ReturnURL
	if returnURL == "" {
		returnURL = callBackAddress + "/api/subscription/alipay/return"
	}

	orderResp, err := client.CreateOrder(&alipay.CreateOrderRequest{
		Scene:       scene,
		OutTradeNo:  tradeNo,
		Subject:     fmt.Sprintf("SUB:%s", plan.Title),
		TotalAmount: strconv.FormatFloat(plan.PriceAmount, 'f', 2, 64),
		ReturnURL:   returnURL,
		AuthToken:   req.AuthToken,
	})
	if err != nil {
		_ = model.ExpireSubscriptionOrder(tradeNo, model.PaymentProviderAlipay)
		common.ApiErrorMsg(c, "拉起支付失败")
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "success", "data": gin.H{
		"pay_url":  orderResp.PayURL,
		"trade_no": tradeNo,
		"type":     string(scene),
	}})
}

// SubscriptionAlipayNotify 支付宝订阅回调
func SubscriptionAlipayNotify(c *gin.Context) {
	if !isAlipayWebhookEnabled() {
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	var params url.Values
	if c.Request.Method == "POST" {
		if err := c.Request.ParseForm(); err != nil {
			_, _ = c.Writer.Write([]byte("fail"))
			return
		}
		params = c.Request.PostForm
	} else {
		params = c.Request.URL.Query()
	}

	if len(params) == 0 {
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	client := GetAlipayClient()
	if client == nil {
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	result, err := client.VerifyAndParseNotify(params)
	if err != nil {
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	_, _ = c.Writer.Write([]byte("success"))

	if result == nil {
		return
	}

	LockOrder(result.OutTradeNo)
	defer UnlockOrder(result.OutTradeNo)

	if err := model.CompleteSubscriptionOrder(result.OutTradeNo, fmt.Sprintf(`{"trade_no":"%s"}`, result.TradeNo), model.PaymentProviderAlipay, model.PaymentMethodAlipay); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("支付宝 订阅订单完成失败 trade_no=%s error=%q", result.OutTradeNo, err.Error()))
	}
}

// SubscriptionAlipayReturn 支付宝订阅浏览器返回
func SubscriptionAlipayReturn(c *gin.Context) {
	var params url.Values
	if c.Request.Method == "POST" {
		if err := c.Request.ParseForm(); err != nil {
			c.Redirect(http.StatusFound, paymentReturnPath("/console/topup?pay=fail"))
			return
		}
		params = c.Request.PostForm
	} else {
		params = c.Request.URL.Query()
	}

	if len(params) == 0 {
		c.Redirect(http.StatusFound, paymentReturnPath("/console/topup?pay=fail"))
		return
	}

	client := GetAlipayClient()
	if client == nil {
		c.Redirect(http.StatusFound, paymentReturnPath("/console/topup?pay=fail"))
		return
	}

	result, err := client.VerifyAndParseNotify(params)
	if err != nil {
		c.Redirect(http.StatusFound, paymentReturnPath("/console/topup?pay=fail"))
		return
	}

	if result != nil {
		LockOrder(result.OutTradeNo)
		defer UnlockOrder(result.OutTradeNo)
		if err := model.CompleteSubscriptionOrder(result.OutTradeNo, fmt.Sprintf(`{"trade_no":"%s"}`, result.TradeNo), model.PaymentProviderAlipay, model.PaymentMethodAlipay); err != nil {
			c.Redirect(http.StatusFound, paymentReturnPath("/console/topup?pay=fail"))
			return
		}
		c.Redirect(http.StatusFound, paymentReturnPath("/console/topup?pay=success"))
		return
	}

	c.Redirect(http.StatusFound, paymentReturnPath("/console/topup?pay=pending"))
}

// ==================== 辅助函数 ====================

func readBody(c *gin.Context) ([]byte, error) {
	return readBodyFromGin(c)
}

func readBodyFromGin(c *gin.Context) ([]byte, error) {
	body := make([]byte, 0)
	buf := make([]byte, 1024)
	for {
		n, err := c.Request.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	return body, nil
}

func extractWeChatPayHeaders(c *gin.Context) map[string]string {
	return map[string]string{
		"Wechatpay-Signature": c.GetHeader("Wechatpay-Signature"),
		"Wechatpay-Timestamp": c.GetHeader("Wechatpay-Timestamp"),
		"Wechatpay-Nonce":     c.GetHeader("Wechatpay-Nonce"),
		"Wechatpay-Serial":    c.GetHeader("Wechatpay-Serial"),
	}
}
