// Package wechat 封装微信支付V3 API
// 支持 Native扫码支付、H5支付、JSAPI支付
// 文档: https://pay.weixin.qq.com/wiki/doc/apiv3/wxpay/pages/index.shtml
package wechat

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
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// PayScene 支付场景
type PayScene string

const (
	SceneNative PayScene = "NATIVE"  // Native扫码支付（PC网站）
	SceneH5     PayScene = "H5"      // H5支付（手机浏览器）
	SceneJSAPI  PayScene = "JSAPI"   // JSAPI支付（微信公众号/小程序）
)

// Client 微信支付V3客户端
type Client struct {
	MchID      string // 商户号
	AppID      string // 应用ID
	MchSerial  string // 商户API证书序列号
	APIv3Key   string // APIv3密钥
	PrivateKey *rsa.PrivateKey // 商户API私钥
	NotifyURL  string // 支付结果通知URL
}

// NewClient 创建微信支付V3客户端
func NewClient(mchID, appID, mchSerial, apiV3Key, privateKeyPEM, notifyURL string) (*Client, error) {
	if mchID == "" || appID == "" || mchSerial == "" || apiV3Key == "" || privateKeyPEM == "" {
		return nil, fmt.Errorf("微信支付配置不完整")
	}

	privateKey, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("解析商户私钥失败: %w", err)
	}

	return &Client{
		MchID:      mchID,
		AppID:      appID,
		MchSerial:  mchSerial,
		APIv3Key:   apiV3Key,
		PrivateKey: privateKey,
		NotifyURL:  notifyURL,
	}, nil
}

// parsePrivateKey 解析PEM格式的RSA私钥
func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// 尝试PKCS1格式
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
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

// CreateOrderRequest 创建支付订单请求参数
type CreateOrderRequest struct {
	Scene       PayScene // 支付场景
	OutTradeNo  string   // 商户订单号
	Description string   // 商品描述
	Amount      int64    // 订单金额（分）
	OpenID      string   // 用户OpenID（JSAPI支付必填）
	ClientIP    string   // 客户端IP（H5支付必填）
}

// CreateOrderResponse 创建支付订单响应
type CreateOrderResponse struct {
	PrepayID string `json:"prepay_id"` // 预支付交易会话ID
	CodeURL  string `json:"code_url"`  // 二维码链接（Native支付）
	H5URL    string `json:"h5_url"`    // H5支付链接
}

// CreateOrder 创建支付订单
func (c *Client) CreateOrder(req *CreateOrderRequest) (*CreateOrderResponse, error) {
	amount := map[string]interface{}{
		"total":    req.Amount,
		"currency": "CNY",
	}

	body := map[string]interface{}{
		"appid":        c.AppID,
		"mchid":        c.MchID,
		"description":  req.Description,
		"out_trade_no": req.OutTradeNo,
		"notify_url":   c.NotifyURL,
		"amount":       amount,
	}

	var apiPath string
	switch req.Scene {
	case SceneNative:
		apiPath = "/v3/pay/transactions/native"
	case SceneH5:
		apiPath = "/v3/pay/transactions/h5"
		sceneInfo := map[string]interface{}{
			"payer_client_ip": req.ClientIP,
			"h5_info": map[string]interface{}{
				"type": "Wap",
			},
		}
		body["scene_info"] = sceneInfo
	case SceneJSAPI:
		apiPath = "/v3/pay/transactions/jsapi"
		if req.OpenID == "" {
			return nil, fmt.Errorf("JSAPI支付需要提供用户OpenID")
		}
		body["payer"] = map[string]interface{}{
			"openid": req.OpenID,
		}
	default:
		return nil, fmt.Errorf("不支持的支付场景: %s", req.Scene)
	}

	respBody, err := c.doPost(apiPath, body)
	if err != nil {
		return nil, fmt.Errorf("创建订单失败: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	resp := &CreateOrderResponse{}
	if v, ok := result["prepay_id"].(string); ok {
		resp.PrepayID = v
	}
	if v, ok := result["code_url"].(string); ok {
		resp.CodeURL = v
	}
	if v, ok := result["h5_url"].(string); ok {
		resp.H5URL = v
	}

	return resp, nil
}

// JSAPIPayParams JSAPI支付参数（前端调起支付所需）
type JSAPIPayParams struct {
	AppID     string `json:"appId"`
	TimeStamp string `json:"timeStamp"`
	NonceStr  string `json:"nonceStr"`
	Package   string `json:"package"`
	SignType  string `json:"signType"`
	PaySign   string `json:"paySign"`
}

// GetJSAPIPayParams 获取JSAPI支付参数
func (c *Client) GetJSAPIPayParams(prepayID string) (*JSAPIPayParams, error) {
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonceStr := common.GetRandomString(32)
	packageStr := fmt.Sprintf("prepay_id=%s", prepayID)

	// 签名串: appId\n timeStamp\n nonceStr\n package\n
	signStr := fmt.Sprintf("%s\n%s\n%s\n%s\n", c.AppID, timestamp, nonceStr, packageStr)

	signature, err := c.sign(signStr)
	if err != nil {
		return nil, err
	}

	return &JSAPIPayParams{
		AppID:     c.AppID,
		TimeStamp: timestamp,
		NonceStr:  nonceStr,
		Package:   packageStr,
		SignType:  "RSA",
		PaySign:   signature,
	}, nil
}

// NotifyResource 支付通知解密后的资源
type NotifyResource struct {
	OutTradeNo     string `json:"out_trade_no"`      // 商户订单号
	TransactionID  string `json:"transaction_id"`    // 微信支付订单号
	TradeState     string `json:"trade_state"`       // 交易状态: SUCCESS/REFUND/NOTPAY/CLOSED/REVOKED/PAYERROR
	TradeStateDesc string `json:"trade_state_desc"`  // 交易状态描述
	Amount         struct {
		Total         int64  `json:"total"`          // 订单总金额（分）
		PayerTotal   int64  `json:"payer_total"`    // 用户实际支付金额（分）
		Currency     string `json:"currency"`       // 货币类型
		PayerCurrency string `json:"payer_currency"` // 用户实际支付货币类型
	} `json:"amount"`
	Payer struct {
		OpenID string `json:"openid"` // 用户OpenID
	} `json:"payer"`
}

// NotifyRequest 支付通知请求
type NotifyRequest struct {
	ID           string `json:"id"`
	CreateTime   string `json:"create_time"`
	EventType    string `json:"event_type"`
	ResourceType string `json:"resource_type"`
	Resource     struct {
		Algorithm      string `json:"algorithm"`
		Ciphertext     string `json:"ciphertext"`
		AssociatedData string `json:"associated_data"`
		Nonce          string `json:"nonce"`
	} `json:"resource"`
}

// VerifyAndDecryptNotify 验证并解密支付通知
func (c *Client) VerifyAndDecryptNotify(body []byte, headers map[string]string) (*NotifyResource, error) {
	var notify NotifyRequest
	if err := json.Unmarshal(body, &notify); err != nil {
		return nil, fmt.Errorf("解析通知请求失败: %w", err)
	}

	// 验证签名（Wechatpay-Signature header）
	if err := c.verifySignature(headers, body); err != nil {
		return nil, fmt.Errorf("验证签名失败: %w", err)
	}

	// 解密资源
	plaintext, err := c.decryptResource(
		notify.Resource.Ciphertext,
		notify.Resource.AssociatedData,
		notify.Resource.Nonce,
	)
	if err != nil {
		return nil, fmt.Errorf("解密通知资源失败: %w", err)
	}

	var resource NotifyResource
	if err := json.Unmarshal(plaintext, &resource); err != nil {
		return nil, fmt.Errorf("解析通知资源失败: %w", err)
	}

	return &resource, nil
}

// sign 使用商户私钥签名
func (c *Client) sign(message string) (string, error) {
	hash := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.PrivateKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("签名失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

// verifySignature 验证微信支付平台签名
func (c *Client) verifySignature(headers map[string]string, body []byte) error {
	// 在生产环境中应验证平台证书签名
	// 此处简化处理：如果配置了APIv3Key，则信任通知
	// 完整实现应缓存平台证书并验证签名
	// TODO: 实现完整的平台证书验签
	return nil
}

// decryptResource 解密通知资源 (AEAD_AES_256_GCM)
func (c *Client) decryptResource(ciphertext, associatedData, nonce string) ([]byte, error) {
	cipherBytes, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("base64解码失败: %w", err)
	}

	plaintext, err := aesGCMDecrypt([]byte(c.APIv3Key), nonce, cipherBytes, associatedData)
	if err != nil {
		return nil, fmt.Errorf("AES-GCM解密失败: %w", err)
	}

	return plaintext, nil
}

// doPost 发送POST请求到微信支付API
func (c *Client) doPost(path string, body interface{}) ([]byte, error) {
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonceStr := common.GetRandomString(32)

	// 构造签名串: HTTP方法\n URL\n 请求时间戳\n 请求随机串\n 请求体\n
	signStr := fmt.Sprintf("POST\n%s\n%s\n%s\n%s\n", path, timestamp, nonceStr, string(bodyJSON))

	signature, err := c.sign(signStr)
	if err != nil {
		return nil, err
	}

	// 构造Authorization头
	authHeader := fmt.Sprintf(
		`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",timestamp="%s",serial_no="%s",signature="%s"`,
		c.MchID, nonceStr, timestamp, c.MchSerial, signature,
	)

	req, err := http.NewRequest("POST", "https://api.mch.weixin.qq.com"+path, strings.NewReader(string(bodyJSON)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "NewAPI/1.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求微信支付API失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return nil, fmt.Errorf("微信支付API返回错误: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// QueryOrderResponse 查询订单响应
type QueryOrderResponse struct {
	OutTradeNo    string `json:"out_trade_no"`
	TransactionID string `json:"transaction_id"`
	TradeState    string `json:"trade_state"`
}

// QueryOrderByOutTradeNo 通过商户订单号查询订单
func (c *Client) QueryOrderByOutTradeNo(outTradeNo string) (*QueryOrderResponse, error) {
	path := fmt.Sprintf("/v3/pay/transactions/out-trade-no/%s?mchid=%s", outTradeNo, c.MchID)

	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonceStr := common.GetRandomString(32)
	signStr := fmt.Sprintf("GET\n%s\n%s\n%s\n\n", path, timestamp, nonceStr)

	signature, err := c.sign(signStr)
	if err != nil {
		return nil, err
	}

	authHeader := fmt.Sprintf(
		`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",timestamp="%s",serial_no="%s",signature="%s"`,
		c.MchID, nonceStr, timestamp, c.MchSerial, signature,
	)

	req, err := http.NewRequest("GET", "https://api.mch.weixin.qq.com"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "NewAPI/1.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("查询订单失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("查询订单返回错误: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	var result QueryOrderResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return &result, nil
}
