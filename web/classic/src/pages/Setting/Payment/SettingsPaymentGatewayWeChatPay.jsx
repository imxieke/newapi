import React, { useEffect, useState, useRef } from 'react';
import { Banner, Button, Form, Spin, Switch, InputNumber, Input, TextArea } from '@douyinfe/semi-ui';
import { API, showError, showSuccess } from '../../../helpers';
import { useTranslation } from 'react-i18next';
import { BookOpen } from 'lucide-react';

export default function SettingsPaymentGatewayWeChatPay(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    WeChatPayEnabled: false,
    WeChatPayMchID: '',
    WeChatPayAppID: '',
    WeChatPayMchSerial: '',
    WeChatPayAPIv3Key: '',
    WeChatPayPrivateKey: '',
    WeChatPayNotifyURL: '',
    WeChatPayMinTopUp: 1,
  });
  const [originInputs, setOriginInputs] = useState({});
  const formApiRef = useRef(null);

  useEffect(() => {
    if (props.options && formApiRef.current) {
      const currentInputs = {
        WeChatPayEnabled: props.options.WeChatPayEnabled === true || props.options.WeChatPayEnabled === 'true',
        WeChatPayMchID: props.options.WeChatPayMchID || '',
        WeChatPayAppID: props.options.WeChatPayAppID || '',
        WeChatPayMchSerial: props.options.WeChatPayMchSerial || '',
        WeChatPayAPIv3Key: props.options.WeChatPayAPIv3Key || '',
        WeChatPayPrivateKey: props.options.WeChatPayPrivateKey || '',
        WeChatPayNotifyURL: props.options.WeChatPayNotifyURL || '',
        WeChatPayMinTopUp: props.options.WeChatPayMinTopUp !== undefined ? parseInt(props.options.WeChatPayMinTopUp) : 1,
      };
      setInputs(currentInputs);
      setOriginInputs({ ...currentInputs });
      formApiRef.current.setValues(currentInputs);
    }
  }, [props.options]);

  const handleFormChange = (values) => {
    setInputs(values);
  };

  const submitWeChatPaySetting = async () => {
    setLoading(true);
    try {
      const options = [
        { key: 'WeChatPayEnabled', value: inputs.WeChatPayEnabled ? 'true' : 'false' },
        { key: 'WeChatPayMchID', value: inputs.WeChatPayMchID },
        { key: 'WeChatPayAppID', value: inputs.WeChatPayAppID },
        { key: 'WeChatPayMchSerial', value: inputs.WeChatPayMchSerial },
        { key: 'WeChatPayAPIv3Key', value: inputs.WeChatPayAPIv3Key },
        { key: 'WeChatPayPrivateKey', value: inputs.WeChatPayPrivateKey },
        { key: 'WeChatPayNotifyURL', value: inputs.WeChatPayNotifyURL },
        { key: 'WeChatPayMinTopUp', value: (inputs.WeChatPayMinTopUp || 1).toString() },
      ];

      const requestQueue = options.map((opt) =>
        API.put('/api/option/', { key: opt.key, value: opt.value }),
      );

      const results = await Promise.all(requestQueue);
      const errorResults = results.filter((res) => !res.data.success);
      if (errorResults.length > 0) {
        errorResults.forEach((res) => showError(res.data.message));
      } else {
        showSuccess(t('更新成功'));
        setOriginInputs({ ...inputs });
        props.refresh?.();
      }
    } catch (error) {
      showError(t('更新失败'));
    }
    setLoading(false);
  };

  return (
    <Spin spinning={loading}>
      <Form
        initValues={inputs}
        onValueChange={handleFormChange}
        getFormApi={(api) => (formApiRef.current = api)}
      >
        <Form.Section text={props.hideSectionTitle ? undefined : t('微信支付V3 设置')}>
          <Banner
            type='info'
            icon={<BookOpen size={16} />}
            description={
              <>
                微信支付V3 商户配置请
                <a href='https://pay.weixin.qq.com/' target='_blank' rel='noreferrer'>点击此处</a>
                登录商户平台获取。支持 Native扫码支付（PC）、H5支付（手机浏览器）、JSAPI支付（微信公众号）。
              </>
            }
          />
          <Form.Switch field='WeChatPayEnabled' label={t('启用微信支付')} />
          <Form.Input field='WeChatPayMchID' label={t('商户号 (mchid)')} placeholder='如: 1900000109' />
          <Form.Input field='WeChatPayAppID' label={t('应用ID (appid)')} placeholder='如: wx8888888888888888' />
          <Form.Input field='WeChatPayMchSerial' label={t('商户API证书序列号')} placeholder='如: 5D2A4E2B3C1D4E5F6A7B8C9D0E1F2A3B' />
          <Form.Input field='WeChatPayAPIv3Key' label={t('APIv3密钥')} mode='password' placeholder='32位APIv3密钥' />
          <Form.TextArea field='WeChatPayPrivateKey' label={t('商户API私钥 (PEM)')} placeholder='-----BEGIN PRIVATE KEY-----&#10;...&#10;-----END PRIVATE KEY-----' rows={4} />
          <Form.Input field='WeChatPayNotifyURL' label={t('支付通知URL (可选)')} placeholder='留空则自动拼接' />
          <Form.InputNumber field='WeChatPayMinTopUp' label={t('最小充值金额(元)')} min={1} step={1} />
          <Button onClick={submitWeChatPaySetting} type='secondary' theme='solid' style={{ marginTop: 12 }}>
            {t('保存微信支付设置')}
          </Button>
        </Form.Section>
      </Form>
    </Spin>
  );
}
