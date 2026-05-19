package setting

// 微信支付V3配置
// 文档: https://pay.weixin.qq.com/wiki/doc/apiv3/wxpay/pages/index.shtml
var (
	WeChatPayEnabled    bool   // 是否启用微信支付
	WeChatPayMchID      string // 商户号 (mchid)
	WeChatPayAppID      string // 应用ID (appid) - 公众号/小程序/APP的appid
	WeChatPayMchSerial  string // 商户API证书序列号
	WeChatPayAPIv3Key   string // APIv3密钥
	WeChatPayPrivateKey string // 商户API私钥 (PEM格式)
	WeChatPayNotifyURL  string // 支付结果通知URL (可选, 为空则自动拼接)
	WeChatPayMinTopUp   int    = 1 // 最小充值金额(元)
)
