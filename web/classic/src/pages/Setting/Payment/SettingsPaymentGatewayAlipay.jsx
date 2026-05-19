import React, { useEffect, useState, useRef } from 'react';
import { Banner, Button, Form, Spin, Switch, InputNumber, Input, TextArea } from '@douyinfe/semi-ui';
import { API, showError, showSuccess } from '../../../helpers';
import { useTranslation } from 'react-i18next';
import { BookOpen } from 'lucide-react';

export default function SettingsPaymentGatewayAlipay(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    AlipayEnabled: false,
    AlipayAppID: '',
    AlipayPrivateKey: '',
    AlipayAlipayPublicKey: '',
    AlipayNotifyURL: '',
    AlipayMinTopUp: 1,
    AlipaySandbox: false,
  });
  const [originInputs, setOriginInputs] = useState({});
  const formApiRef = useRef(null);

  useEffect(() => {
    if (props.options && formApiRef.current) {
      const currentInputs = {
        AlipayEnabled: props.options.AlipayEnabled === true || props.options.AlipayEnabled === 'true',
        AlipayAppID: props.options.AlipayAppID || '',
        AlipayPrivateKey: props.options.AlipayPrivateKey || '',
        AlipayAlipayPublicKey: props.options.AlipayAlipayPublicKey || '',
        AlipayNotifyURL: props.options.AlipayNotifyURL || '',
        AlipayMinTopUp: props.options.AlipayMinTopUp !== undefined ? parseInt(props.options.AlipayMinTopUp) : 1,
        AlipaySandbox: props.options.AlipaySandbox === true || props.options.AlipaySandbox === 'true',
      };
      setInputs(currentInputs);
      setOriginInputs({ ...currentInputs });
      formApiRef.current.setValues(currentInputs);
    }
  }, [props.options]);

  const handleFormChange = (values) => {
    setInputs(values);
  };

  const submitAlipaySetting = async () => {
    setLoading(true);
    try {
      const options = [
        { key: 'AlipayEnabled', value: inputs.AlipayEnabled ? 'true' : 'false' },
        { key: 'AlipayAppID', value: inputs.AlipayAppID },
        { key: 'AlipayPrivateKey', value: inputs.AlipayPrivateKey },
        { key: 'AlipayAlipayPublicKey', value: inputs.AlipayAlipayPublicKey },
        { key: 'AlipayNotifyURL', value: inputs.AlipayNotifyURL },
        { key: 'AlipayMinTopUp', value: (inputs.AlipayMinTopUp || 1).toString() },
        { key: 'AlipaySandbox', value: inputs.AlipaySandbox ? 'true' : 'false' },
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
        <Form.Section text={props.hideSectionTitle ? undefined : t('支付宝V3 设置')}>
          <Banner
            type='info'
            icon={<BookOpen size={16} />}
            description={
              <>
                支付宝应用配置请
                <a href='https://open.alipay.com/develop/manage' target='_blank' rel='noreferrer'>点击此处</a>
                登录开放平台获取。支持电脑网站支付（Page）、手机网站支付（WAP）、JSAPI支付。
              </>
            }
          />
          <Form.Switch field='AlipayEnabled' label={t('启用支付宝')} />
          <Form.Input field='AlipayAppID' label={t('应用ID (app_id)')} placeholder='如: 2021000000000001' />
          <Form.TextArea field='AlipayPrivateKey' label={t('应用私钥 (RSA2)')} placeholder='应用私钥' rows={3} />
          <Form.TextArea field='AlipayAlipayPublicKey' label={t('支付宝公钥')} placeholder='支付宝公钥' rows={3} />
          <Form.Input field='AlipayNotifyURL' label={t('支付通知URL (可选)')} placeholder='留空则自动拼接' />
          <Form.InputNumber field='AlipayMinTopUp' label={t('最小充值金额(元)')} min={1} step={1} />
          <Form.Switch field='AlipaySandbox' label={t('沙箱环境')} />
          <Button onClick={submitAlipaySetting} type='secondary' theme='solid' style={{ marginTop: 12 }}>
            {t('保存支付宝设置')}
          </Button>
        </Form.Section>
      </Form>
    </Spin>
  );
}
