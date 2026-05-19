// Package alipay 封装支付宝V3 API
// 支持电脑网站支付(AlipayTradePagePay)、手机网站支付(AlipayTradeWapPay)、JSAPI支付
// 文档: https://opendocs.alipay.com/open-v3/doc
package alipay

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// PayScene 支付场景
type PayScene string

const (
	ScenePage PayScene = "page"  // 电脑网站支付
	SceneWap  PayScene = "wap"   // 手机网站支付
	SceneJSAPI PayScene = "jsapi" // JSAPI支付（生活号/小程序）
)

// Client 支付宝V3客户端
type Client struct {
	AppID           string // 应用ID
	PrivateKey      *rsa.PrivateKey // 应用私钥
	AlipayPublicKey string // 支付宝公钥
	NotifyURL       string // 支付结果通知URL
	Sandbox         bool   // 是否沙箱环境
}

// NewClient 创建支付宝V3客户端
func NewClient(appID, privateKeyStr, alipayPublicKey, notifyURL string, sandbox bool) (*Client, error) {
	if appID == "" || privateKeyStr == "" || alipayPublicKey == "" {
		return nil, fmt.Errorf("支付宝配置不完整")
	}

	privateKey, err := parsePrivateKey(privateKeyStr)
	if err != nil {
		return nil, fmt.Errorf("解析应用私钥失败: %w", err)
	}

	return &Client{
		AppID:           appID,
		PrivateKey:      privateKey,
		AlipayPublicKey: alipayPublicKey,
		NotifyURL:       notifyURL,
		Sandbox:         sandbox,
	}, nil
}

// parsePrivateKey 解析PEM格式的RSA私钥
func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	// 尝试PEM格式
	block, _ := pem.Decode([]byte(pemStr))
	var keyBytes []byte
	if block != nil {
		keyBytes = block.Bytes
	} else {
		// 可能是纯base64编码的DER格式
		decoded, err := base64.StdEncoding.DecodeString(pemStr)
		if err != nil {
			return nil, fmt.Errorf("failed to decode private key")
		}
		keyBytes = decoded
	}

	key, err := x509.ParsePKCS8PrivateKey(keyBytes)
	if err != nil {
		key, err = x509.ParsePKCS1PrivateKey(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key: %w", err)
		}
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an RSA private key")
	}

	return rsaKey, nil
}

// getGatewayURL 获取支付宝网关URL
func (c *Client) getGatewayURL() string {
	if c.Sandbox {
		return "https://openapi-sandbox.d.alipay.com/gateway.do"
	}
	return "https://openapi.alipay.com/gateway.do"
}

// CreateOrderRequest 创建支付订单请求参数
type CreateOrderRequest struct {
	Scene       PayScene // 支付场景
	OutTradeNo  string   // 商户订单号
	Subject     string   // 订单标题
	TotalAmount string   // 订单金额（元，如 "0.01"）
	ReturnURL   string   // 支付完成后跳转URL
	AuthToken   string   // 用户授权令牌（JSAPI支付必填）
}

// CreateOrderResponse 创建支付订单响应
type CreateOrderResponse struct {
	PayURL string `json:"pay_url"` // 支付跳转URL
}

// CreateOrder 创建支付订单（返回支付跳转URL）
func (c *Client) CreateOrder(req *CreateOrderRequest) (*CreateOrderResponse, error) {
	var method string
	var bizContent map[string]interface{}

	bizContent = map[string]interface{}{
		"out_trade_no": req.OutTradeNo,
		"subject":      req.Subject,
		"total_amount": req.TotalAmount,
		"product_code": "FAST_INSTANT_TRADE_PAY",
	}

	switch req.Scene {
	case ScenePage:
		method = "alipay.trade.page.pay"
		bizContent["product_code"] = "FAST_INSTANT_TRADE_PAY"
	case SceneWap:
		method = "alipay.trade.wap.pay"
		bizContent["product_code"] = "QUICK_WAP_WAY"
	case SceneJSAPI:
		method = "alipay.trade.jsapi.pay"
		bizContent["product_code"] = "JSAPI_PAY"
		if req.AuthToken == "" {
			return nil, fmt.Errorf("JSAPI支付需要提供用户授权令牌")
		}
	default:
		return nil, fmt.Errorf("不支持的支付场景: %s", req.Scene)
	}

	bizContentJSON, _ := json.Marshal(bizContent)

	params := map[string]string{
		"app_id":        c.AppID,
		"method":        method,
		"format":        "JSON",
		"return_url":    req.ReturnURL,
		"charset":       "utf-8",
		"sign_type":     "RSA2",
		"timestamp":     time.Now().Format("2006-01-02 15:04:05"),
		"version":       "1.0",
		"notify_url":    c.NotifyURL,
		"biz_content":   string(bizContentJSON),
	}

	// 计算签名
	sign, err := c.sign(params)
	if err != nil {
		return nil, fmt.Errorf("签名失败: %w", err)
	}
	params["sign"] = sign

	// 构造跳转URL
	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}

	payURL := c.getGatewayURL() + "?" + values.Encode()

	return &CreateOrderResponse{
		PayURL: payURL,
	}, nil
}

// NotifyResult 支付通知结果
type NotifyResult struct {
	OutTradeNo  string `json:"out_trade_no"`   // 商户订单号
	TradeNo     string `json:"trade_no"`       // 支付宝交易号
	TradeStatus string `json:"trade_status"`   // 交易状态: TRADE_SUCCESS/TRADE_FINISHED/WAIT_BUYER_PAY
	TotalAmount string `json:"total_amount"`   // 订单金额
	BuyerID     string `json:"buyer_id"`       // 买家支付宝用户号
}

// VerifyAndParseNotify 验证并解析支付通知
func (c *Client) VerifyAndParseNotify(params url.Values) (*NotifyResult, error) {
	// 提取签名
	sign := params.Get("sign")
	signType := params.Get("sign_type")
	if sign == "" {
		return nil, fmt.Errorf("通知缺少签名")
	}

	// 验证签名
	if err := c.verifyNotifySign(params, sign, signType); err != nil {
		return nil, fmt.Errorf("验证签名失败: %w", err)
	}

	// 检查交易状态
	tradeStatus := params.Get("trade_status")
	if tradeStatus != "TRADE_SUCCESS" && tradeStatus != "TRADE_FINISHED" {
		return nil, nil // 非成功状态，返回nil表示忽略
	}

	result := &NotifyResult{
		OutTradeNo:  params.Get("out_trade_no"),
		TradeNo:     params.Get("trade_no"),
		TradeStatus: tradeStatus,
		TotalAmount: params.Get("total_amount"),
		BuyerID:     params.Get("buyer_id"),
	}

	return result, nil
}

// sign 计算签名
func (c *Client) sign(params map[string]string) (string, error) {
	// 按key排序拼接参数
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "sign" || k == "sign_type" {
			continue
		}
		keys = append(keys, k)
	}
	// 简单排序
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}

	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(params[k])
		sb.WriteString("&")
	}
	signStr := sb.String()
	if len(signStr) > 0 {
		signStr = signStr[:len(signStr)-1] // 去掉末尾&
	}

	hash := sha256.Sum256([]byte(signStr))
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.PrivateKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("RSA签名失败: %w", err)
	}

	return base64.StdEncoding.EncodeToString(signature), nil
}

// verifyNotifySign 验证支付宝通知签名
func (c *Client) verifyNotifySign(params url.Values, sign, signType string) error {
	// 按key排序拼接参数（排除sign和sign_type）
	keys := make([]string, 0)
	for k := range params {
		if k == "sign" || k == "sign_type" {
			continue
		}
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}

	var sb strings.Builder
	for _, k := range keys {
		v := params.Get(k)
		if v == "" {
			continue
		}
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(v)
		sb.WriteString("&")
	}
	signStr := sb.String()
	if len(signStr) > 0 {
		signStr = signStr[:len(signStr)-1]
	}

	// 解码签名
	signBytes, err := base64.StdEncoding.DecodeString(sign)
	if err != nil {
		return fmt.Errorf("base64解码签名失败: %w", err)
	}

	// 解析支付宝公钥
	pubKey, err := parsePublicKey(c.AlipayPublicKey)
	if err != nil {
		return fmt.Errorf("解析支付宝公钥失败: %w", err)
	}

	hash := sha256.Sum256([]byte(signStr))
	err = rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hash[:], signBytes)
	if err != nil {
		return fmt.Errorf("RSA验签失败: %w", err)
	}

	return nil
}

// parsePublicKey 解析支付宝公钥
func parsePublicKey(keyStr string) (*rsa.PublicKey, error) {
	// 尝试PEM格式
	block, _ := pem.Decode([]byte(keyStr))
	var keyBytes []byte
	if block != nil {
		keyBytes = block.Bytes
	} else {
		// 支付宝公钥通常是纯base64编码
		decoded, err := base64.StdEncoding.DecodeString(keyStr)
		if err != nil {
			return nil, fmt.Errorf("failed to decode public key")
		}
		keyBytes = decoded
	}

	pub, err := x509.ParsePKIXPublicKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not an RSA public key")
	}

	return rsaPub, nil
}

// QueryTradeResponse 查询交易响应
type QueryTradeResponse struct {
	OutTradeNo  string `json:"out_trade_no"`
	TradeNo     string `json:"trade_no"`
	TradeStatus string `json:"trade_status"`
	TotalAmount string `json:"total_amount"`
}

// QueryTradeByOutTradeNo 通过商户订单号查询交易
func (c *Client) QueryTradeByOutTradeNo(outTradeNo string) (*QueryTradeResponse, error) {
	bizContent, _ := json.Marshal(map[string]string{
		"out_trade_no": outTradeNo,
	})

	params := map[string]string{
		"app_id":      c.AppID,
		"method":      "alipay.trade.query",
		"format":      "JSON",
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   time.Now().Format("2006-01-02 15:04:05"),
		"version":     "1.0",
		"biz_content": string(bizContent),
	}

	sign, err := c.sign(params)
	if err != nil {
		return nil, err
	}
	params["sign"] = sign

	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}

	req, err := http.NewRequest("POST", c.getGatewayURL(), strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("查询交易失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	// 解析响应
	var result map[string]json.RawMessage
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	queryKey := fmt.Sprintf("alipay_trade_query_response")
	if raw, ok := result[queryKey]; ok {
		var queryResp struct {
			Code         string `json:"code"`
			OutTradeNo   string `json:"out_trade_no"`
			TradeNo      string `json:"trade_no"`
			TradeStatus  string `json:"trade_status"`
			TotalAmount  string `json:"total_amount"`
		}
		if err := json.Unmarshal(raw, &queryResp); err != nil {
			return nil, fmt.Errorf("解析查询响应失败: %w", err)
		}
		if queryResp.Code != "10000" {
			return nil, fmt.Errorf("查询交易返回错误: code=%s", queryResp.Code)
		}
		return &QueryTradeResponse{
			OutTradeNo:  queryResp.OutTradeNo,
			TradeNo:     queryResp.TradeNo,
			TradeStatus: queryResp.TradeStatus,
			TotalAmount: queryResp.TotalAmount,
		}, nil
	}

	return nil, fmt.Errorf("响应中缺少查询结果")
}

// GetRandomString 生成随机字符串（如果common包不可用时的备用）
func GetRandomString(length int) string {
	return common.GetRandomString(length)
}
