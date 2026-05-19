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
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// AlipayRequest 支付宝充值请求
type AlipayRequest struct {
	Amount    int64  `json:"amount"`     // 充值额度
	Scene     string `json:"scene"`      // 支付场景: page/wap/jsapi
	ReturnURL string `json:"return_url"` // 支付完成后跳转URL
	AuthToken string `json:"auth_token"` // 用户授权令牌(JSAPI必填)
}

// GetAlipayClient 获取支付宝客户端
func GetAlipayClient() *alipay.Client {
	if !setting.AlipayEnabled {
		return nil
	}
	if setting.AlipayAppID == "" || setting.AlipayPrivateKey == "" || setting.AlipayAlipayPublicKey == "" {
		return nil
	}

	notifyURL := setting.AlipayNotifyURL
	if notifyURL == "" {
		callBackAddress := service.GetCallbackAddress()
		notifyURL = callBackAddress + "/api/alipay/notify"
	}

	client, err := alipay.NewClient(
		setting.AlipayAppID,
		setting.AlipayPrivateKey,
		setting.AlipayAlipayPublicKey,
		notifyURL,
		setting.AlipaySandbox,
	)
	if err != nil {
		common.SysError("支付宝客户端初始化失败: " + err.Error())
		return nil
	}
	return client
}

// RequestAlipay 发起支付宝充值
func RequestAlipay(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	var req AlipayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	// 校验最小充值金额
	minTopup := int64(setting.AlipayMinTopUp)
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
	var scene alipay.PayScene
	switch req.Scene {
	case "wap":
		scene = alipay.SceneWap
	case "jsapi":
		scene = alipay.SceneJSAPI
	default:
		scene = alipay.ScenePage
	}

	// 生成订单号
	tradeNo := fmt.Sprintf("ALI%s%d", common.GetRandomString(6), time.Now().Unix())
	tradeNo = fmt.Sprintf("USR%dNO%s", id, tradeNo)

	client := GetAlipayClient()
	if client == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付宝未配置"})
		return
	}

	// 构造returnURL
	returnURL := req.ReturnURL
	if returnURL == "" {
		returnURL = paymentReturnPath("/console/topup?pay=pending")
	}

	// 创建支付订单
	orderResp, err := client.CreateOrder(&alipay.CreateOrderRequest{
		Scene:       scene,
		OutTradeNo:  tradeNo,
		Subject:     fmt.Sprintf("TUC%d", req.Amount),
		TotalAmount: strconv.FormatFloat(payMoney, 'f', 2, 64),
		ReturnURL:   returnURL,
		AuthToken:   req.AuthToken,
	})
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("支付宝 拉起支付失败 user_id=%d trade_no=%s scene=%s amount=%d error=%q", id, tradeNo, scene, req.Amount, err.Error()))
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
		PaymentMethod:   model.PaymentMethodAlipay,
		PaymentProvider: model.PaymentProviderAlipay,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("支付宝 创建充值订单失败 user_id=%d trade_no=%s error=%q", id, tradeNo, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("支付宝 充值订单创建成功 user_id=%d trade_no=%s scene=%s amount=%d money=%.2f pay_url=%q", id, tradeNo, scene, req.Amount, payMoney, orderResp.PayURL))
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": gin.H{
		"pay_url":  orderResp.PayURL,
		"trade_no": tradeNo,
		"type":     string(scene),
	}})
}

// AlipayNotify 支付宝回调通知
func AlipayNotify(c *gin.Context) {
	if !isAlipayWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("支付宝 webhook 被拒绝 path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	// 解析参数（GET或POST）
	var params url.Values
	if c.Request.Method == "POST" {
		if err := c.Request.ParseForm(); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("支付宝 webhook POST表单解析失败 error=%q", err.Error()))
			_, _ = c.Writer.Write([]byte("fail"))
			return
		}
		params = c.Request.PostForm
	} else {
		params = c.Request.URL.Query()
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("支付宝 webhook 收到请求 path=%q client_ip=%s method=%s", c.Request.RequestURI, c.ClientIP(), c.Request.Method))

	if len(params) == 0 {
		logger.LogWarn(c.Request.Context(), "支付宝 webhook 参数为空")
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	client := GetAlipayClient()
	if client == nil {
		logger.LogError(c.Request.Context(), "支付宝 client 未初始化")
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	// 验证并解析通知
	result, err := client.VerifyAndParseNotify(params)
	if err != nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("支付宝 webhook 验签失败 client_ip=%s error=%q", c.ClientIP(), err.Error()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	// 验签成功，先返回success
	_, _ = c.Writer.Write([]byte("success"))

	// result为nil表示非成功状态，忽略
	if result == nil {
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("支付宝 webhook 验签成功 trade_no=%s trade_status=%s client_ip=%s", result.OutTradeNo, result.TradeStatus, c.ClientIP()))

	// 处理支付成功
	LockOrder(result.OutTradeNo)
	defer UnlockOrder(result.OutTradeNo)

	if err := model.RechargeAlipay(result.OutTradeNo, c.ClientIP()); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("支付宝 充值失败 trade_no=%s error=%q", result.OutTradeNo, err.Error()))
	} else {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("支付宝 充值成功 trade_no=%s", result.OutTradeNo))
	}
}

// AlipayReturn 支付宝浏览器跳转返回
func AlipayReturn(c *gin.Context) {
	// 解析参数
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

	// 验签
	result, err := client.VerifyAndParseNotify(params)
	if err != nil {
		c.Redirect(http.StatusFound, paymentReturnPath("/console/topup?pay=fail"))
		return
	}

	if result != nil {
		// 支付成功，完成订单
		LockOrder(result.OutTradeNo)
		defer UnlockOrder(result.OutTradeNo)
		if err := model.RechargeAlipay(result.OutTradeNo, c.ClientIP()); err != nil {
			c.Redirect(http.StatusFound, paymentReturnPath("/console/topup?pay=fail"))
			return
		}
		c.Redirect(http.StatusFound, paymentReturnPath("/console/topup?pay=success"))
		return
	}

	c.Redirect(http.StatusFound, paymentReturnPath("/console/topup?pay=pending"))
}

// RequestAlipayAmount 查询支付宝实付金额
func RequestAlipayAmount(c *gin.Context) {
	var req AmountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	minTopup := int64(setting.AlipayMinTopUp)
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
