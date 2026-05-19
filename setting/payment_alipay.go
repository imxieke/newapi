package setting

// 支付宝V3配置
// 文档: https://opendocs.alipay.com/open-v3/doc
var (
	AlipayEnabled      bool   // 是否启用支付宝
	AlipayAppID        string // 应用ID (app_id)
	AlipayPrivateKey   string // 应用私钥 (RSA2)
	AlipayAlipayPublicKey string // 支付宝公钥
	AlipayNotifyURL    string // 支付结果通知URL (可选, 为空则自动拼接)
	AlipayMinTopUp     int    = 1 // 最小充值金额(元)
	AlipaySandbox      bool   // 是否沙箱环境
)
