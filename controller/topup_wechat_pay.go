package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/payment/wechat"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// WeChatPayRequest 微信支付充值请求
type WeChatPayRequest struct {
	Amount  int64  `json:"amount"`   // 充值额度
	Scene   string `json:"scene"`    // 支付场景: native/h5/jsapi
	OpenID  string `json:"open_id"`  // 用户OpenID (JSAPI必填)
	ClientIP string `json:"client_ip"` // 客户端IP (H5必填)
}

// GetWeChatPayClient 获取微信支付客户端
func GetWeChatPayClient() *wechat.Client {
	if !setting.WeChatPayEnabled {
		return nil
	}
	if setting.WeChatPayMchID == "" || setting.WeChatPayAppID == "" ||
		setting.WeChatPayMchSerial == "" || setting.WeChatPayAPIv3Key == "" ||
		setting.WeChatPayPrivateKey == "" {
		return nil
	}

	notifyURL := setting.WeChatPayNotifyURL
	if notifyURL == "" {
		callBackAddress := service.GetCallbackAddress()
		notifyURL = callBackAddress + "/api/wechat-pay/notify"
	}

	client, err := wechat.NewClient(
		setting.WeChatPayMchID,
		setting.WeChatPayAppID,
		setting.WeChatPayMchSerial,
		setting.WeChatPayAPIv3Key,
		setting.WeChatPayPrivateKey,
		notifyURL,
	)
	if err != nil {
		common.SysError("微信支付客户端初始化失败: " + err.Error())
		return nil
	}
	return client
}

// RequestWeChatPay 发起微信支付充值
func RequestWeChatPay(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	var req WeChatPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	// 校验最小充值金额
	minTopup := int64(setting.WeChatPayMinTopUp)
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dMinTopup := decimal.NewFromInt(minTopup)
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		minTopup = dMinTopup.Mul(dQuotaPerUnit).IntPart()
	}
	if req.Amount < minTopup {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", minTopup)})
		return
	}

	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	payMoney := getPayMoney(req.Amount, group)
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
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

	// 生成订单号
	tradeNo := fmt.Sprintf("WX%s%d", common.GetRandomString(6), time.Now().Unix())
	tradeNo = fmt.Sprintf("USR%dNO%s", id, tradeNo)

	client := GetWeChatPayClient()
	if client == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "微信支付未配置"})
		return
	}

	// 金额转换为分
	amountInFen := int(payMoney * 100)

	// 创建支付订单
	orderResp, err := client.CreateOrder(&wechat.CreateOrderRequest{
		Scene:       scene,
		OutTradeNo:  tradeNo,
		Description: fmt.Sprintf("TUC%d", req.Amount),
		Amount:      int64(amountInFen),
		OpenID:      req.OpenID,
		ClientIP:    req.ClientIP,
	})
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 拉起支付失败 user_id=%d trade_no=%s scene=%s amount=%d error=%q", id, tradeNo, scene, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	// 计算充值额度
	amount := req.Amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dAmount := decimal.NewFromInt(int64(amount))
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		amount = dAmount.Div(dQuotaPerUnit).IntPart()
	}

	// 创建TopUp记录
	topUp := &model.TopUp{
		UserId:          id,
		Amount:          amount,
		Money:           payMoney,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodWeChatPay,
		PaymentProvider: model.PaymentProviderWeChatPay,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 创建充值订单失败 user_id=%d trade_no=%s error=%q", id, tradeNo, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	// 构造响应
	data := gin.H{
		"trade_no": tradeNo,
	}

	switch scene {
	case wechat.SceneNative:
		data["code_url"] = orderResp.CodeURL // 二维码链接
		data["type"] = "native"
	case wechat.SceneH5:
		data["h5_url"] = orderResp.H5URL // H5支付链接
		data["type"] = "h5"
	case wechat.SceneJSAPI:
		// 获取JSAPI支付参数
		payParams, err := client.GetJSAPIPayParams(orderResp.PrepayID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取支付参数失败"})
			return
		}
		data["jsapi_params"] = payParams
		data["type"] = "jsapi"
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("微信支付 充值订单创建成功 user_id=%d trade_no=%s scene=%s amount=%d money=%.2f", id, tradeNo, scene, req.Amount, payMoney))
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": data})
}

// WeChatPayNotify 微信支付回调通知
func WeChatPayNotify(c *gin.Context) {
	if !isWeChatPayWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("微信支付 webhook 被拒绝 path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		c.JSON(http.StatusForbidden, gin.H{"code": "FAIL", "message": "webhook disabled"})
		return
	}

	// 读取请求体
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 webhook 读取请求体失败 error=%q", err.Error()))
		c.JSON(http.StatusBadRequest, gin.H{"code": "FAIL", "message": "read body failed"})
		return
	}

	// 提取headers用于验签
	headers := map[string]string{
		"Wechatpay-Signature":  c.GetHeader("Wechatpay-Signature"),
		"Wechatpay-Timestamp":  c.GetHeader("Wechatpay-Timestamp"),
		"Wechatpay-Nonce":      c.GetHeader("Wechatpay-Nonce"),
		"Wechatpay-Serial":     c.GetHeader("Wechatpay-Serial"),
		"HTTP_WECHATPAY_SIGNATURE": c.GetHeader("HTTP_WECHATPAY_SIGNATURE"),
	}

	client := GetWeChatPayClient()
	if client == nil {
		logger.LogError(c.Request.Context(), "微信支付 client 未初始化")
		c.JSON(http.StatusInternalServerError, gin.H{"code": "FAIL", "message": "client not initialized"})
		return
	}

	// 验证并解密通知
	resource, err := client.VerifyAndDecryptNotify(body, headers)
	if err != nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("微信支付 webhook 验签/解密失败 client_ip=%s error=%q", c.ClientIP(), err.Error()))
		c.JSON(http.StatusBadRequest, gin.H{"code": "FAIL", "message": "verification failed"})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("微信支付 webhook 收到通知 trade_no=%s trade_state=%s client_ip=%s", resource.OutTradeNo, resource.TradeState, c.ClientIP()))

	// 先返回成功，再处理业务逻辑
	c.JSON(http.StatusOK, gin.H{"code": "SUCCESS", "message": "成功"})

	// 处理支付成功
	if resource.TradeState == "SUCCESS" {
		LockOrder(resource.OutTradeNo)
		defer UnlockOrder(resource.OutTradeNo)

		if err := model.RechargeWeChatPay(resource.OutTradeNo, c.ClientIP()); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("微信支付 充值失败 trade_no=%s error=%q", resource.OutTradeNo, err.Error()))
		} else {
			logger.LogInfo(c.Request.Context(), fmt.Sprintf("微信支付 充值成功 trade_no=%s", resource.OutTradeNo))
		}
	}
}

// RequestWeChatPayAmount 查询微信支付实付金额
func RequestWeChatPayAmount(c *gin.Context) {
	var req AmountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	minTopup := int64(setting.WeChatPayMinTopUp)
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dMinTopup := decimal.NewFromInt(minTopup)
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		minTopup = dMinTopup.Mul(dQuotaPerUnit).IntPart()
	}
	if req.Amount < minTopup {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", minTopup)})
		return
	}

	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	payMoney := getPayMoney(req.Amount, group)
	if payMoney <= 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": strconv.FormatFloat(payMoney, 'f', 2, 64)})
}

// WeChatPayOrderStatus 查询微信支付订单状态（前端轮询）
func WeChatPayOrderStatus(c *gin.Context) {
	tradeNo := c.Query("trade_no")
	if tradeNo == "" {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "缺少订单号"})
		return
	}

	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "订单不存在"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"status": topUp.Status,
		},
	})
}

// parseWeChatPayNotifyBody 解析微信支付通知body（辅助函数）
func parseWeChatPayNotifyBody(body []byte) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}
