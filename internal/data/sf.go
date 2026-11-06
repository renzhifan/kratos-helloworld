package data

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"logisticsServer/internal/biz"
	"logisticsServer/internal/conf"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	// sfAccessTokenPath 获取访问令牌接口路径，拼接在网关地址后面
	sfAccessTokenPath = "/oauth2/accessToken"
	// sfGrantType 申请类型，固定 password
	sfGrantType = "password"
	// sfResultCodeSuccess 顺丰接口响应成功码
	sfResultCodeSuccess = "A1000"
	// sfAccessTokenCacheKey 访问令牌缓存 key
	sfAccessTokenCacheKey = "sf:access_token"
	// sfAccessTokenLockKey 刷新访问令牌的分布式锁 key
	sfAccessTokenLockKey = "sf:access_token:lock"
	// sfAccessTokenLockTTL 分布式锁过期时间，避免持锁实例异常时死锁
	sfAccessTokenLockTTL = 60 * time.Second
	// sfAccessTokenLockWait 未抢到锁时等待令牌落缓存的最长时间
	sfAccessTokenLockWait = 3 * time.Second
	// sfAccessTokenLockPoll 未抢到锁时轮询缓存的间隔
	sfAccessTokenLockPoll = 200 * time.Millisecond
	// sfAccessTokenExpireAhead 令牌过期提前量（秒），避免边界时间失效
	sfAccessTokenExpireAhead = 20
	// sfUnlockTimeout 释放分布式锁的超时时间
	sfUnlockTimeout = 3 * time.Second
	// sfHTTPTimeout 请求顺丰网关超时时间
	sfHTTPTimeout = 30 * time.Second
	// sfStdServicePath 顺丰统一入口接口路径，拼接在网关地址后面
	sfStdServicePath = "/std/service"
	// sfServiceCodeSearchOrderResp 订单结果查询接口服务代码
	sfServiceCodeSearchOrderResp = "EXP_RECE_SEARCH_ORDER_RESP"
	// sfServiceCodeQueryDeliverTm 时效标准及价格查询接口服务代码
	sfServiceCodeQueryDeliverTm = "EXP_RECE_QUERY_DELIVERTM"
	// sfServiceCodeCreateOrder 下单接口服务代码
	sfServiceCodeCreateOrder = "EXP_RECE_CREATE_ORDER"
	// sfServiceCodeUpdateOrder 订单确认/取消接口服务代码
	sfServiceCodeUpdateOrder = "EXP_RECE_UPDATE_ORDER"
	// sfServiceCodeWantedIntercept 截单转寄退回接口服务代码
	sfServiceCodeWantedIntercept = "EXP_RECE_WANTED_INTERCEPT"
	// sfServiceCodeFilterOrder 订单筛选接口服务代码
	sfServiceCodeFilterOrder = "EXP_RECE_FILTER_ORDER_BSP"
	// sfFilterTypeAuto 默认筛单类别：自动筛单
	sfFilterTypeAuto = 1
	// sfDefaultCountry 默认国家代码，用于配置的默认寄件人
	sfDefaultCountry = "CN"
	// sfDefaultLanguage 顺丰响应报文默认语言，不传时为中文简体
	sfDefaultLanguage = "zh-CN"
	// sfStdResultCodeSuccess 顺丰统一入口业务成功码
	sfStdResultCodeSuccess = "S0000"
	// sfSuccessTrue / sfSuccessFalse 响应体的 success 字段，用字符串返回以保证失败时也能输出
	sfSuccessTrue  = "true"
	sfSuccessFalse = "false"
)

// sfUnlockScript 仅释放自己持有的锁，避免误删其他实例的锁
var sfUnlockScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("del", KEYS[1])
end
return 0
`)

// sfAccessTokenResp 顺丰访问令牌响应
type sfAccessTokenResp struct {
	ApiResponseID string `json:"apiResponseID"` // 响应ID
	ApiResultCode string `json:"apiResultCode"` // 响应码
	ApiErrorMsg   string `json:"apiErrorMsg"`   // 错误描述
	AccessToken   string `json:"accessToken"`   // 访问令牌
	ExpiresIn     int    `json:"expiresIn"`     // 访问令牌过期时间，单位（秒）
}

// sfOrderSearchMsg 订单结果查询业务报文（EXP_RECE_SEARCH_ORDER_RESP）
type sfOrderSearchMsg struct {
	OrderID       string `json:"orderId,omitempty"`       // 客户订单号
	SearchType    string `json:"searchType,omitempty"`    // 查询类型：1正向单 2退货单
	Language      string `json:"language,omitempty"`      // 响应报文语言
	MainWaybillNo string `json:"mainWaybillNo,omitempty"` // 母单号
}

// sfDeliverTmMsg 时效标准及价格查询业务报文（EXP_RECE_QUERY_DELIVERTM）
type sfDeliverTmMsg struct {
	BusinessType  string     `json:"businessType,omitempty"`  // 时效代码，为空时返回默认时效对应的产品列表
	ConsignedTime string     `json:"consignedTime,omitempty"` // 寄件时间
	SrcAddress    *sfAddress `json:"srcAddress"`              // 原寄地地址
	DestAddress   *sfAddress `json:"destAddress"`             // 目的地地址
	SearchPrice   string     `json:"searchPrice,omitempty"`   // 是否查询价格：1查询 0不查询
	Weight        float64    `json:"weight,omitempty"`        // 货物总重量，单位kg，必须>0
	Volume        float64    `json:"volume,omitempty"`        // 货物体积，按厘米计算
	MonthlyCard   string     `json:"monthlyCard,omitempty"`   // 月结卡号，查询个性化预估价格
}

// sfAddress 顺丰地址信息
type sfAddress struct {
	Province string `json:"province,omitempty"` // 省份
	City     string `json:"city,omitempty"`     // 城市
	District string `json:"district,omitempty"` // 区/县
	Address  string `json:"address,omitempty"`  // 详细地址
	Code     string `json:"code,omitempty"`     // 地区域代码，填写后忽略省市区具体地址
}

// sfCreateOrderMsg 下单业务报文（EXP_RECE_CREATE_ORDER）
type sfCreateOrderMsg struct {
	OrderID         string          `json:"orderId"`               // 客户订单号
	MonthlyCard     string          `json:"monthlyCard,omitempty"` // 月结卡号
	Language        string          `json:"language,omitempty"`    // 语言
	ContactInfoList []sfContactInfo `json:"contactInfoList"`       // 收寄双方信息
	CargoDetails    []sfCargoDetail `json:"cargoDetails"`          // 货物信息
	ServiceList     []sfServiceItem `json:"serviceList,omitempty"` // 增值服务
}

// sfContactInfo 收寄双方信息
type sfContactInfo struct {
	ContactType int32  `json:"contactType"`        // 1寄件人 2收件人
	Contact     string `json:"contact"`            // 联系人姓名
	Company     string `json:"company,omitempty"`  // 公司名称
	Tel         string `json:"tel"`                // 联系电话
	Country     string `json:"country,omitempty"`  // 国家代码
	PostCode    string `json:"postCode,omitempty"` // 邮编
	Address     string `json:"address"`            // 详细地址
}

// sfCargoDetail 货物信息
type sfCargoDetail struct {
	Name       string  `json:"name"`                 // 货物名称
	Count      float64 `json:"count,omitempty"`      // 货物数量
	Unit       string  `json:"unit,omitempty"`       // 货物单位
	Weight     float64 `json:"weight,omitempty"`     // 货物重量，单位kg
	Amount     float64 `json:"amount,omitempty"`     // 货物金额
	Currency   string  `json:"currency,omitempty"`   // 币种
	SourceArea string  `json:"sourceArea,omitempty"` // 原产地
}

// sfServiceItem 增值服务
type sfServiceItem struct {
	Name   string `json:"name"`             // 增值服务名称
	Value  string `json:"value,omitempty"`  // 增值服务值
	Value1 string `json:"value1,omitempty"` // 附加属性1
	Value2 string `json:"value2,omitempty"` // 附加属性2
}

// sfUpdateOrderMsg 订单确认/取消业务报文（EXP_RECE_UPDATE_ORDER）
type sfUpdateOrderMsg struct {
	OrderID           string          `json:"orderId"`            // 客户订单号
	DealType          int32           `json:"dealType"`           // 1确认 2取消
	Language          string          `json:"language,omitempty"` // 语言
	TotalWeight       float64         `json:"totalWeight"`        // 订单总重量，确认时必填，取消时可传 0
	WaybillNoInfoList []sfWaybillInfo `json:"waybillNoInfoList"`  // 顺丰运单号信息，取消时可为空数组
}

// sfWaybillInfo 运单号信息，下单与查询返回的运单号信息共用一个结构
type sfWaybillInfo struct {
	WaybillType int32   `json:"waybillType"`      // 运单号类型：1母单 2子单 3退回件
	WaybillNo   string  `json:"waybillNo"`        // 运单号
	BoxNo       string  `json:"boxNo,omitempty"`  // 箱号，下单返回
	Length      float64 `json:"length,omitempty"` // 长，单位cm，下单返回
	Width       float64 `json:"width,omitempty"`  // 宽，单位cm，下单返回
	Height      float64 `json:"height,omitempty"` // 高，单位cm，下单返回
	Weight      float64 `json:"weight,omitempty"` // 重量，单位kg，下单返回
	Volume      float64 `json:"volume,omitempty"` // 体积，单位cm³，下单返回
}

// sfWantedInterceptMsg 截单转寄退回业务报文（EXP_RECE_WANTED_INTERCEPT）
type sfWantedInterceptMsg struct {
	WaybillNo      string            `json:"waybillNo"`                // 运单号
	ServiceCode    string            `json:"serviceCode"`              // 指令类型：1转寄 2退回 3优派 4再派 等
	Role           string            `json:"role"`                     // 发起方：1寄方 2收方
	PayMode        string            `json:"payMode"`                  // 付款方式：1寄付现结 2到付现结 3寄付转第三方月结 4寄付月结
	MonthlyCardNo  string            `json:"monthlyCardNo,omitempty"`  // 月结卡号
	ProductType    string            `json:"productType,omitempty"`    // 产品类型
	CodAmount      *float64          `json:"codAmount,omitempty"`      // 代收货款金额，修改代收货款时传入
	DeliverDate    string            `json:"deliverDate,omitempty"`    // 派送日期，格式 yyyy-MM-dd
	DeliverTimeMin string            `json:"deliverTimeMin,omitempty"` // 派送最早时间，格式 HH:mm
	DeliverTimeMax string            `json:"deliverTimeMax,omitempty"` // 派送最晚时间，格式 HH:mm
	SelfPickPoint  string            `json:"selfPickPoint,omitempty"`  // 自取点：1合作点 2快递柜 3网点 4中转场
	NewDestAddress *sfNewDestAddress `json:"newDestAddress,omitempty"` // 新目的地信息，取消指令时不传
	Cancel         bool              `json:"cancel"`                   // true 取消转寄/退回指令，false 下发指令
	CusID          string            `json:"cusId,omitempty"`          // 取消指令时传指令下发返回的指令编码
}

// sfNewDestAddress 新目的地信息，转寄/退回必须携带地址、联系人姓名与电话
type sfNewDestAddress struct {
	Country      string `json:"country,omitempty"`      // 国家
	CountryCode  string `json:"countryCode,omitempty"`  // 国家编码
	Province     string `json:"province,omitempty"`     // 省
	City         string `json:"city,omitempty"`         // 市
	County       string `json:"county,omitempty"`       // 区
	Address      string `json:"address,omitempty"`      // 详细地址，不含省市区
	Company      string `json:"company,omitempty"`      // 公司
	Contact      string `json:"contact,omitempty"`      // 联系人姓名
	Phone        string `json:"phone,omitempty"`        // 联系电话
	AreaCode     string `json:"areaCode,omitempty"`     // 网点代码
	LocationCode string `json:"locationCode,omitempty"` // 城市代码
}

// sfFilterOrderMsg 订单筛选业务报文（EXP_RECE_FILTER_ORDER_BSP），顺丰要求 msgData 为数组
type sfFilterOrderMsg struct {
	FilterType   int32              `json:"filterType"`             // 筛单类别：1自动筛单 2可人工筛单
	OrderID      string             `json:"orderId,omitempty"`      // 客户订单号
	MonthlyCard  string             `json:"monthlyCard,omitempty"`  // 月结卡号
	ContactInfos []sfFilterAddrInfo `json:"contactInfos,omitempty"` // 地址信息
}

// sfFilterAddrInfo 筛选地址信息
type sfFilterAddrInfo struct {
	ContactType int32  `json:"contactType,omitempty"` // 地址类型：1寄件方 2到件方
	Tel         string `json:"tel,omitempty"`         // 联系电话
	Country     string `json:"country,omitempty"`     // 国家或地区代码，跨境件必传
	Province    string `json:"province,omitempty"`    // 省级行政区名称
	City        string `json:"city,omitempty"`        // 地级行政区名称
	County      string `json:"county,omitempty"`      // 县/区级行政区名称
	Address     string `json:"address,omitempty"`     // 详细地址
	PostCode    string `json:"postCode,omitempty"`    // 邮编，跨境件必填
}

// sfStdServiceResp 顺丰统一入口响应
// 数字签名方式外层为 {success,errorCode,errorMsg,msgData}；
// OAuth2 方式外层为 {apiResultCode,apiResultData}，apiResultData 内层才是业务报文
type sfStdServiceResp struct {
	Success       bool            `json:"success"`       // 是否成功（数字签名方式）
	ErrorCode     string          `json:"errorCode"`     // 业务错误码（数字签名方式）
	ErrorMsg      string          `json:"errorMsg"`      // 业务错误描述（数字签名方式）
	MsgData       json.RawMessage `json:"msgData"`       // 业务响应报文
	ApiResponseID string          `json:"apiResponseID"` // 响应ID
	ApiResultCode string          `json:"apiResultCode"` // 网关响应码，A1000表示受理成功
	ApiErrorMsg   string          `json:"apiErrorMsg"`   // 网关错误描述
	ApiResultData string          `json:"apiResultData"` // 内层业务报文，JSON字符串
}

// sfWantedInterceptResult 截单转寄退回业务结果，顺丰部分环境下 msgData 内还会再包一层同名结果
type sfWantedInterceptResult struct {
	ErrorCode string                     `json:"errorCode"`               // 业务错误码，S0000 表示成功
	ErrorMsg  string                     `json:"errorMsg"`                // 业务错误描述
	CusID     string                     `json:"cusId"`                   // 指令编码
	Amount    sfFlexString               `json:"amount"`                  // 费用参数，顺丰返回字符串或数字
	Freight   *sfFreightAdditionInfoResp `json:"freightAdditionInfoResp"` // 费用附加信息
	MsgData   json.RawMessage            `json:"msgData"`                 // 内层业务结果
}

// sfFreightAdditionInfoResp 费用附加信息
type sfFreightAdditionInfoResp struct {
	InterceptionDeptCode string `json:"interceptionDeptCode"` // 预估拦截地
	TransferFlg          string `json:"transferFlg"`          // 拦截地属性是否中转场：1中转场，其余值非中转场
}

// sfFlexString 顺丰同名字段存在字符串与数字两种写法，统一收为字符串
type sfFlexString string

func (s *sfFlexString) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*s = sfFlexString(str)
		return nil
	}
	// 非字符串时保留原始文本，如 0
	*s = sfFlexString(data)
	return nil
}

// sfOrderSearchResp 订单结果查询业务响应（EXP_RECE_SEARCH_ORDER_RESP）
type sfOrderSearchResp struct {
	OrderID                string             `json:"orderId"`                // 客户订单号
	MainWaybillNo          string             `json:"mainWaybillNo"`          // 顺丰运单号（母单号）
	WaybillNoInfoList      []sfWaybillInfo    `json:"waybillNoInfoList"`      // 运单号信息列表
	ResStatus              int32              `json:"resStatus"`              // 订单状态
	OriginCode             string             `json:"origincode"`             // 原寄地代码
	DestCode               string             `json:"destcode"`               // 目的地代码
	FilterResult           string             `json:"filterResult"`           // 筛单结果
	Remark                 json.RawMessage    `json:"remark"`                 // 备注，透传顺丰原始报文
	RouteLabelInfo         []sfRouteLabelInfo `json:"routeLabelInfo"`         // 路由标签信息（面单数据）
	ContactInfo            json.RawMessage    `json:"contactInfo"`            // 联系方式信息，透传顺丰原始报文
	ClientCode             string             `json:"clientCode"`             // 客户端编码
	ServiceList            json.RawMessage    `json:"serviceList"`            // 服务列表，透传顺丰原始报文
	ReturnExtraInfoList    json.RawMessage    `json:"returnExtraInfoList"`    // 返回扩展信息列表，透传顺丰原始报文
	DeliverConsigneeRemark string             `json:"deliverConsigneeRemark"` // 派送备注
	IsDelete               string             `json:"isDelete"`               // 订单是否删除
	BackWaybillNo          string             `json:"backWaybillNo"`          // 退货单运单号
	TotalQty               int32              `json:"totalQty"`               // 总件数
}

// sfDeliverTmResp 时效标准及价格查询业务响应（EXP_RECE_QUERY_DELIVERTM）
type sfDeliverTmResp struct {
	DeliverTmDto []sfDeliverTmOffer `json:"deliverTmDto"` // 时效与价格信息列表
}

// sfDeliverTmOffer 时效与价格信息
type sfDeliverTmOffer struct {
	BusinessType     string  `json:"businessType"`     // 快件产品（时效代码）
	BusinessTypeDesc string  `json:"businessTypeDesc"` // 快件产品描述
	DeliverTime      string  `json:"deliverTime"`      // 承诺时间
	SearchPrice      string  `json:"searchPrice"`      // 是否查询价格：1返回价格 0不返回价格
	Fee              float64 `json:"fee"`              // 价格，单位元
	CloseTime        string  `json:"closeTime"`        // 截单时间
}

// sfCreateOrderResp 下单业务响应（EXP_RECE_CREATE_ORDER）
type sfCreateOrderResp struct {
	OrderID                   string             `json:"orderId"`                   // 客户订单号
	OriginCode                string             `json:"originCode"`                // 原寄地代码
	DestCode                  string             `json:"destCode"`                  // 目的地代码
	FilterResult              int32              `json:"filterResult"`              // 筛单结果
	Remark                    string             `json:"remark"`                    // 备注
	URL                       string             `json:"url"`                       // 面单打印地址
	PaymentLink               string             `json:"paymentLink"`               // 支付链接
	IsUpstairs                bool               `json:"isUpstairs"`                // 是否上楼
	IsSpecialWarehouseService bool               `json:"isSpecialWarehouseService"` // 是否特殊仓服务
	MappingMark               string             `json:"mappingMark"`               // 映射标记
	AgentMailno               string             `json:"agentMailno"`               // 代理运单号
	ReturnExtraInfoList       json.RawMessage    `json:"returnExtraInfoList"`       // 返回扩展信息列表，透传顺丰原始报文
	WaybillNoInfoList         []sfWaybillInfo    `json:"waybillNoInfoList"`         // 运单号信息列表
	RouteLabelInfo            []sfRouteLabelInfo `json:"routeLabelInfo"`            // 路由标签信息（面单数据）
	ContactInfoList           json.RawMessage    `json:"contactInfoList"`           // 收寄双方信息，透传顺丰原始报文
	SendStartTm               string             `json:"sendStartTm"`               // 发货时间
	CustomerRights            json.RawMessage    `json:"customerRights"`            // 客户权益，透传顺丰原始报文
	ExpressTypeID             json.RawMessage    `json:"expressTypeId"`             // 产品类型ID，透传顺丰原始报文
}

// sfUpdateOrderResp 订单确认/取消业务响应（EXP_RECE_UPDATE_ORDER）
type sfUpdateOrderResp struct {
	OrderID           string          `json:"orderId"`           // 客户订单号
	WaybillNoInfoList []sfWaybillInfo `json:"waybillNoInfoList"` // 顺丰运单号信息
	ResStatus         int32           `json:"resStatus"`         // 结果状态
	ExtraInfoList     json.RawMessage `json:"extraInfoList"`     // 扩展属性，透传顺丰原始报文
}

// sfFilterOrderResp 订单筛选业务响应（EXP_RECE_FILTER_ORDER_BSP）
type sfFilterOrderResp struct {
	ResDtos []sfFilterOrderResult `json:"resDtos"` // 筛选结果列表，顺序与请求一致
}

// sfFilterOrderResult 单个订单的筛选结果
type sfFilterOrderResult struct {
	OrderID      string `json:"orderId"`      // 客户订单号
	FilterResult int32  `json:"filterResult"` // 筛选结果：1人工确认 2可收派 3不可以收派 4地址无法确认
	OriginCode   string `json:"originCode"`   // 原寄地区域代码，可收派时不为空
	DestCode     string `json:"destCode"`     // 目的地区域代码，可收派时不为空
	Remark       string `json:"remark"`       // 不可以收派的原因代码
}

// sfRouteLabelInfo 路由标签信息
type sfRouteLabelInfo struct {
	Code       string            `json:"code"`           // 返回码，1000表示成功
	RouteLabel *sfRouteLabelData `json:"routeLabelData"` // 路由标签数据（面单数据）
	Message    string            `json:"message"`        // 返回信息
}

// sfRouteLabelData 路由标签数据
type sfRouteLabelData struct {
	WaybillNo              string          `json:"waybillNo"`              // 运单号
	SourceTransferCode     string          `json:"sourceTransferCode"`     // 原寄地中转场代码
	SourceCityCode         string          `json:"sourceCityCode"`         // 原寄地城市代码
	SourceDeptCode         string          `json:"sourceDeptCode"`         // 原寄地网点代码
	SourceTeamCode         string          `json:"sourceTeamCode"`         // 原寄地分部代码
	DestCityCode           string          `json:"destCityCode"`           // 目的地城市代码
	DestDeptCode           string          `json:"destDeptCode"`           // 目的地网点代码
	DestDeptCodeMapping    string          `json:"destDeptCodeMapping"`    // 目的地网点代码映射
	DestTeamCode           string          `json:"destTeamCode"`           // 目的地分部代码
	DestTeamCodeMapping    string          `json:"destTeamCodeMapping"`    // 目的地分部代码映射
	DestTransferCode       string          `json:"destTransferCode"`       // 目的地中转场代码
	DestRouteLabel         string          `json:"destRouteLabel"`         // 目的地路由标签
	ProName                string          `json:"proName"`                // 产品名称
	ProCode                string          `json:"proCode"`                // 产品编码
	CargoTypeCode          string          `json:"cargoTypeCode"`          // 货物类型编码
	LimitTypeCode          string          `json:"limitTypeCode"`          // 限制类型编码
	ExpressTypeCode        string          `json:"expressTypeCode"`        // 快递类型编码
	CodingMapping          string          `json:"codingMapping"`          // 编码映射
	CodingMappingOut       string          `json:"codingMappingOut"`       // 外部编码映射
	XBFlag                 string          `json:"xbFlag"`                 // XB标识
	PrintFlag              string          `json:"printFlag"`              // 打印标识
	PrintIcon              string          `json:"printIcon"`              // 打印图标
	TwoDimensionCode       string          `json:"twoDimensionCode"`       // 二维码内容
	ABFlag                 string          `json:"abFlag"`                 // AB标识
	DestPortCode           string          `json:"destPortCode"`           // 目的地港口代码
	DestCountry            string          `json:"destCountry"`            // 目的地国家
	DestPostCode           string          `json:"destPostCode"`           // 目的地邮编
	GoodsValueTotal        string          `json:"goodsValueTotal"`        // 货物总价值
	CurrencySymbol         string          `json:"currencySymbol"`         // 货币符号
	CusBatch               string          `json:"cusBatch"`               // 客户批次
	GoodsNumber            string          `json:"goodsNumber"`            // 货物数量
	ErrMsg                 string          `json:"errMsg"`                 // 错误信息
	CheckCode              string          `json:"checkCode"`              // 校验码
	ProIcon                string          `json:"proIcon"`                // 产品图标
	FileIcon               string          `json:"fileIcon"`               // 文件图标
	FbaIcon                string          `json:"fbaIcon"`                // FBA图标
	IcsmIcon               string          `json:"icsmIcon"`               // ICSM图标
	DestGisDeptCode        string          `json:"destGisDeptCode"`        // 目的地GIS网点代码
	NewIcon                json.RawMessage `json:"newIcon"`                // 新图标，透传顺丰原始报文
	DestAddrKeyWord        string          `json:"destAddrKeyWord"`        // 目的地地址关键字
	SendAreaCode           string          `json:"sendAreaCode"`           // 寄件区域代码
	DestinationStationCode string          `json:"destinationStationCode"` // 目的站点代码
	SxLabelDestCode        string          `json:"sxLabelDestCode"`        // 时效标签目的地代码
	SxDestTransferCode     string          `json:"sxDestTransferCode"`     // 时效标签目的地中转场代码
	SxCompany              string          `json:"sxCompany"`              // 时效标签公司
	NewABFlag              string          `json:"newAbFlag"`              // 新AB标识
	RongType               json.RawMessage `json:"rongType"`               // 融通类型，透传顺丰原始报文
	WaybillIconList        json.RawMessage `json:"waybillIconList"`        // 运单图标列表，透传顺丰原始报文
}

type sfRepo struct {
	data   *Data
	logger *slog.Logger
	conf   *conf.Sf
	cli    *http.Client
}

// NewSfRepo creates a SfRepo backed by the SF Express open platform gateway.
func NewSfRepo(data *Data, logger *slog.Logger, c *conf.Sf) biz.SfRepo {
	return &sfRepo{
		data:   data,
		logger: logger,
		conf:   c,
		// otelhttp 让每次网关调用都产生 client span 挂在当前请求 trace 下，
		// 令牌申请与业务接口的耗时才看得见；span 名用方法与路径便于定位。
		cli: &http.Client{
			Timeout: sfHTTPTimeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport,
				otelhttp.WithSpanNameFormatter(func(_ string, req *http.Request) string {
					return req.Method + " " + req.URL.Path
				}),
			),
		},
	}
}

// GetAccessToken 获取顺丰访问令牌，优先取缓存，缓存失效后抢锁重新申请
func (r *sfRepo) GetAccessToken(ctx context.Context) (*biz.SfToken, error) {
	if token, remain, err := r.getCachedToken(ctx); err != nil {
		return nil, err
	} else if token != "" {
		return &biz.SfToken{AccessToken: token, ExpiresIn: remain}, nil
	}

	// 抢锁刷新，避免缓存失效瞬间并发请求穿透到顺丰网关
	lockValue := strconv.FormatInt(time.Now().UnixNano(), 10)
	locked, err := r.data.rdb.SetNX(ctx, sfAccessTokenLockKey, lockValue, sfAccessTokenLockTTL).Result()
	if err != nil {
		r.logger.ErrorContext(ctx, "lock sf access token failed", "err", err)
		return nil, biz.ErrSfCacheFailed.WithCause(err)
	}
	if !locked {
		// 未抢到锁，等待持锁实例把令牌写入缓存
		return r.waitCachedToken(ctx)
	}
	defer r.unlock(ctx, lockValue)

	// 拿到锁后再查一次，避免等待锁期间令牌已被其他实例刷新
	if token, remain, err := r.getCachedToken(ctx); err != nil {
		return nil, err
	} else if token != "" {
		return &biz.SfToken{AccessToken: token, ExpiresIn: remain}, nil
	}

	return r.requestAccessToken(ctx)
}

// GetConfig 获取顺丰调用配置，访问令牌走统一缓存，其余字段取自 sf 配置段，校验码不对外暴露
func (r *sfRepo) GetConfig(ctx context.Context) (*biz.SfConfig, error) {
	token, err := r.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return &biz.SfConfig{
		Token:        token,
		PartnerID:    r.conf.GetPartnerID(),
		MonthlyCard:  r.conf.GetCustID(),
		TemplateCode: r.conf.GetTemplateCode(),
	}, nil
}

// getCachedToken 读取缓存中的访问令牌，token 为空表示未命中，remain 为剩余有效秒数
func (r *sfRepo) getCachedToken(ctx context.Context) (string, int32, error) {
	// GET 与 TTL 用 pipeline 合并成一次往返，命中缓存时不再多花一个 RTT
	pipe := r.data.rdb.Pipeline()
	tokenCmd := pipe.Get(ctx, sfAccessTokenCacheKey)
	ttlCmd := pipe.TTL(ctx, sfAccessTokenCacheKey)
	if _, err := pipe.Exec(ctx); err != nil && !stderrors.Is(err, redis.Nil) {
		r.logger.ErrorContext(ctx, "read sf access token cache failed", "err", err)
		return "", 0, biz.ErrSfCacheFailed.WithCause(err)
	}
	token, err := tokenCmd.Result()
	if err != nil {
		if stderrors.Is(err, redis.Nil) {
			return "", 0, nil
		}
		r.logger.ErrorContext(ctx, "read sf access token cache failed", "err", err)
		return "", 0, biz.ErrSfCacheFailed.WithCause(err)
	}
	var remain int32
	if ttl, ttlErr := ttlCmd.Result(); ttlErr == nil && ttl > 0 {
		remain = int32(ttl.Seconds())
	}
	return token, remain, nil
}

// waitCachedToken 未抢到锁时轮询等待持锁实例刷新缓存
func (r *sfRepo) waitCachedToken(ctx context.Context) (*biz.SfToken, error) {
	ticker := time.NewTicker(sfAccessTokenLockPoll)
	defer ticker.Stop()
	timer := time.NewTimer(sfAccessTokenLockWait)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, biz.ErrSfTokenRequest.WithCause(ctx.Err())
		case <-timer.C:
			r.logger.ErrorContext(ctx, "wait for sf access token timeout")
			return nil, biz.ErrSfTokenRequest
		case <-ticker.C:
			if token, remain, err := r.getCachedToken(ctx); err != nil {
				return nil, err
			} else if token != "" {
				return &biz.SfToken{AccessToken: token, ExpiresIn: remain}, nil
			}
		}
	}
}

// requestAccessToken 调用顺丰鉴权接口申请访问令牌并写入缓存
func (r *sfRepo) requestAccessToken(ctx context.Context) (*biz.SfToken, error) {
	if r.conf == nil || r.conf.PartnerID == "" || r.conf.Secret == "" || r.conf.Host == "" {
		r.logger.ErrorContext(ctx, "sf open platform config is missing")
		return nil, biz.ErrSfConfigInvalid
	}

	params := url.Values{}
	params.Set("partnerID", r.conf.PartnerID)
	params.Set("secret", r.conf.Secret)
	params.Set("grantType", sfGrantType)

	reqURL := strings.TrimRight(r.conf.Host, "/") + sfAccessTokenPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(params.Encode()))
	if err != nil {
		r.logger.ErrorContext(ctx, "build sf access token request failed", "err", err)
		return nil, biz.ErrSfTokenRequest.WithCause(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")

	resp, err := r.cli.Do(req)
	if err != nil {
		r.logger.ErrorContext(ctx, "request sf access token failed", "err", err)
		return nil, biz.ErrSfTokenRequest.WithCause(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		r.logger.ErrorContext(ctx, "read sf access token response failed", "err", err)
		return nil, biz.ErrSfTokenRequest.WithCause(err)
	}

	var tokenResp sfAccessTokenResp
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		r.logger.ErrorContext(ctx, "decode sf access token response failed", "err", err, "body", string(body))
		return nil, biz.ErrSfTokenRequest.WithCause(err)
	}

	if tokenResp.ApiResultCode != sfResultCodeSuccess {
		r.logger.ErrorContext(ctx, "sf auth failed", "apiResultCode", tokenResp.ApiResultCode, "apiErrorMsg", tokenResp.ApiErrorMsg)
		return nil, biz.ErrSfAuthFailed
	}
	if tokenResp.AccessToken == "" {
		r.logger.ErrorContext(ctx, "sf returned empty access token", "apiResponseID", tokenResp.ApiResponseID)
		return nil, biz.ErrSfTokenEmpty
	}

	// 提前 sfAccessTokenExpireAhead 秒过期，避免临界时间使用失效令牌
	expireIn := tokenResp.ExpiresIn - sfAccessTokenExpireAhead
	if expireIn < 1 {
		expireIn = tokenResp.ExpiresIn
	}
	if expireIn < 1 {
		// 顺丰未返回有效期时不做缓存，避免令牌被永久缓存
		r.logger.WarnContext(ctx, "sf returned invalid token ttl, skip caching", "apiResponseID", tokenResp.ApiResponseID)
		return &biz.SfToken{AccessToken: tokenResp.AccessToken}, nil
	}

	token := &biz.SfToken{AccessToken: tokenResp.AccessToken, ExpiresIn: int32(expireIn)}
	if err := r.data.rdb.Set(ctx, sfAccessTokenCacheKey, tokenResp.AccessToken, time.Duration(expireIn)*time.Second).Err(); err != nil {
		// 令牌本身可用，缓存失败只记录日志，不影响本次返回
		r.logger.ErrorContext(ctx, "cache sf access token failed", "err", err)
	}
	return token, nil
}

// unlock 释放访问令牌锁
func (r *sfRepo) unlock(ctx context.Context, lockValue string) {
	// 请求可能已结束或取消：WithoutCancel 保留 trace 关联并脱钩取消信号，
	// 再单独套一层超时，保证解锁既不会被提前中断，也不会阻塞返回。
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sfUnlockTimeout)
	defer cancel()
	if err := sfUnlockScript.Run(ctx, r.data.rdb, []string{sfAccessTokenLockKey}, lockValue).Err(); err != nil {
		r.logger.ErrorContext(ctx, "release sf access token lock failed", "err", err)
	}
}

// ExpReceSearchOrderResp 订单结果查询，用于未收到下单返回数据时查询客户订单当前的处理状态
func (r *sfRepo) ExpReceSearchOrderResp(ctx context.Context, query *biz.SfOrderSearchQuery) (*biz.SfOrderSearchResult, error) {
	msgData, err := r.callStdService(ctx, sfServiceCodeSearchOrderResp, r.buildSearchOrderMsg(query))
	if err != nil {
		// 顺丰业务错误作为正常响应返回，调用方直接读 success/errorCode/errorMsg
		if remote, ok := asSfRemoteError(err); ok {
			return &biz.SfOrderSearchResult{Success: sfSuccessFalse, ErrorCode: remote.Code, ErrorMsg: remote.Message}, nil
		}
		return nil, err
	}

	var resp sfOrderSearchResp
	if err := r.decodeStdMsgData(ctx, msgData, &resp); err != nil {
		return nil, err
	}
	reply := toBizOrderSearchResult(&resp)
	reply.Success = sfSuccessTrue
	reply.ErrorCode = sfStdResultCodeSuccess
	return reply, nil
}

// buildSearchOrderMsg 组装订单结果查询报文，语言不传时缺省 zh-CN
func (r *sfRepo) buildSearchOrderMsg(query *biz.SfOrderSearchQuery) *sfOrderSearchMsg {
	msg := &sfOrderSearchMsg{
		OrderID:       query.OrderID,
		SearchType:    query.SearchType,
		Language:      query.Language,
		MainWaybillNo: query.MainWaybillNo,
	}
	if msg.Language == "" {
		msg.Language = sfDefaultLanguage
	}
	return msg
}

// ExpReceQueryDeliverTm 时效标准及价格查询，根据寄收地址、重量与业务类型查询预计送达时间与运费
func (r *sfRepo) ExpReceQueryDeliverTm(ctx context.Context, query *biz.SfDeliverTmQuery) (*biz.SfDeliverTmResult, error) {
	msgData, err := r.callStdService(ctx, sfServiceCodeQueryDeliverTm, r.buildQueryDeliverTmMsg(query))
	if err != nil {
		// 顺丰业务错误作为正常响应返回，调用方直接读 success/errorCode/errorMsg
		if remote, ok := asSfRemoteError(err); ok {
			return &biz.SfDeliverTmResult{Success: sfSuccessFalse, ErrorCode: remote.Code, ErrorMsg: remote.Message}, nil
		}
		return nil, err
	}

	var resp sfDeliverTmResp
	if err := r.decodeStdMsgData(ctx, msgData, &resp); err != nil {
		return nil, err
	}
	reply := toBizDeliverTmResult(&resp)
	reply.Success = sfSuccessTrue
	reply.ErrorCode = sfStdResultCodeSuccess
	return reply, nil
}

// buildQueryDeliverTmMsg 组装时效价格查询报文，月结卡号缺失时取配置，否则查不到个性化预估价格
func (r *sfRepo) buildQueryDeliverTmMsg(query *biz.SfDeliverTmQuery) *sfDeliverTmMsg {
	msg := &sfDeliverTmMsg{
		BusinessType:  query.BusinessType,
		ConsignedTime: query.ConsignedTime,
		SrcAddress:    buildSfAddress(query.SrcAddress),
		DestAddress:   buildSfAddress(query.DestAddress),
		SearchPrice:   query.SearchPrice,
		Volume:        query.Volume,
		MonthlyCard:   query.MonthlyCard,
	}
	// 重量为非必传字段，未传时不参与组包
	if query.Weight != nil {
		msg.Weight = *query.Weight
	}
	// 月结卡号未传时取配置，月结账号才能拿到协议价格
	if msg.MonthlyCard == "" {
		msg.MonthlyCard = r.conf.GetCustID()
	}
	return msg
}

// buildSfAddress 转换寄收地址，nil 时返回 nil
func buildSfAddress(a *biz.SfAddress) *sfAddress {
	if a == nil {
		return nil
	}
	return &sfAddress{
		Province: a.Province,
		City:     a.City,
		District: a.District,
		Address:  a.Address,
		Code:     a.Code,
	}
}

// ExpReceCreateOrder 下单，支持月结卡号与代收货款等增值服务
func (r *sfRepo) ExpReceCreateOrder(ctx context.Context, order *biz.SfCreateOrder) (*biz.SfCreateOrderResult, error) {
	msgData, err := r.callStdService(ctx, sfServiceCodeCreateOrder, r.buildCreateOrderMsg(order))
	if err != nil {
		// 顺丰业务错误作为正常响应返回，调用方直接读 success/errorCode/errorMsg
		if remote, ok := asSfRemoteError(err); ok {
			return &biz.SfCreateOrderResult{Success: sfSuccessFalse, ErrorCode: remote.Code, ErrorMsg: remote.Message}, nil
		}
		return nil, err
	}

	var resp sfCreateOrderResp
	if err := r.decodeStdMsgData(ctx, msgData, &resp); err != nil {
		return nil, err
	}
	reply := toBizCreateOrderResult(&resp)
	reply.Success = sfSuccessTrue
	reply.ErrorCode = sfStdResultCodeSuccess
	return reply, nil
}

// buildCreateOrderMsg 组装下单报文，月结卡号与寄件人缺失时取配置默认值
func (r *sfRepo) buildCreateOrderMsg(order *biz.SfCreateOrder) *sfCreateOrderMsg {
	msg := &sfCreateOrderMsg{
		OrderID:     order.OrderID,
		MonthlyCard: order.MonthlyCard,
		Language:    order.Language,
	}
	// 月结卡号未传时取配置，月结/货到付款下单必填
	if msg.MonthlyCard == "" {
		msg.MonthlyCard = r.conf.GetCustID()
	}

	hasSender := false
	for _, c := range order.ContactInfos {
		if c.ContactType == biz.SfContactTypeSender {
			hasSender = true
		}
		msg.ContactInfoList = append(msg.ContactInfoList, sfContactInfo{
			ContactType: c.ContactType,
			Contact:     c.Contact,
			Company:     c.Company,
			Tel:         c.Tel,
			Country:     c.Country,
			PostCode:    c.PostCode,
			Address:     c.Address,
		})
	}
	// 未传寄件人时用配置的默认寄件人补齐，顺丰要求收寄双方信息完整
	if !hasSender {
		if sender := r.buildDefaultSender(); sender != nil {
			msg.ContactInfoList = append(msg.ContactInfoList, *sender)
		}
	}

	for _, c := range order.CargoDetails {
		msg.CargoDetails = append(msg.CargoDetails, sfCargoDetail{
			Name:       c.Name,
			Count:      c.Count,
			Unit:       c.Unit,
			Weight:     c.Weight,
			Amount:     c.Amount,
			Currency:   c.Currency,
			SourceArea: c.SourceArea,
		})
	}

	for _, s := range order.ServiceList {
		msg.ServiceList = append(msg.ServiceList, sfServiceItem{
			Name:   s.Name,
			Value:  s.Value,
			Value1: s.Value1,
			Value2: s.Value2,
		})
	}
	return msg
}

// buildDefaultSender 用配置的默认寄件人组装寄件人信息，未配置时返回 nil
func (r *sfRepo) buildDefaultSender() *sfContactInfo {
	sender := r.conf.GetSender()
	if sender.GetName() == "" || sender.GetMobile() == "" {
		return nil
	}
	// 顺丰寄件地址要求省市区与详细地址拼接为完整文本
	address := sender.GetProvince() + sender.GetCity() + sender.GetCounty() + sender.GetAddress()
	return &sfContactInfo{
		ContactType: biz.SfContactTypeSender,
		Contact:     sender.GetName(),
		Tel:         sender.GetMobile(),
		Country:     sfDefaultCountry,
		Address:     address,
	}
}

// ExpReceUpdateOrder 订单确认/取消，dealType 1确认 2取消
func (r *sfRepo) ExpReceUpdateOrder(ctx context.Context, order *biz.SfUpdateOrder) (*biz.SfUpdateOrderResult, error) {
	msgData, err := r.callStdService(ctx, sfServiceCodeUpdateOrder, r.buildUpdateOrderMsg(order))
	if err != nil {
		// 顺丰业务错误作为正常响应返回，调用方直接读 success/errorCode/errorMsg
		if remote, ok := asSfRemoteError(err); ok {
			return &biz.SfUpdateOrderResult{Success: sfSuccessFalse, ErrorCode: remote.Code, ErrorMsg: remote.Message}, nil
		}
		return nil, err
	}

	var resp sfUpdateOrderResp
	if err := r.decodeStdMsgData(ctx, msgData, &resp); err != nil {
		return nil, err
	}
	reply := toBizUpdateOrderResult(&resp)
	reply.Success = sfSuccessTrue
	reply.ErrorCode = sfStdResultCodeSuccess
	return reply, nil
}

// buildUpdateOrderMsg 组装订单确认/取消报文，取消时可不传运单号，但仍需以空数组传递给顺丰
func (r *sfRepo) buildUpdateOrderMsg(order *biz.SfUpdateOrder) *sfUpdateOrderMsg {
	msg := &sfUpdateOrderMsg{
		OrderID:           order.OrderID,
		DealType:          order.DealType,
		Language:          order.Language,
		TotalWeight:       order.TotalWeight,
		WaybillNoInfoList: make([]sfWaybillInfo, 0, len(order.WaybillNoInfos)),
	}
	for _, w := range order.WaybillNoInfos {
		msg.WaybillNoInfoList = append(msg.WaybillNoInfoList, sfWaybillInfo{
			WaybillType: w.WaybillType,
			WaybillNo:   w.WaybillNo,
		})
	}
	return msg
}

// ExpReceWantedIntercept 截单转寄退回，对在途运单下发转寄、退回、改派等拦截指令
func (r *sfRepo) ExpReceWantedIntercept(ctx context.Context, order *biz.SfInterceptOrder) (*biz.SfInterceptResult, error) {
	msgData, err := r.callStdService(ctx, sfServiceCodeWantedIntercept, r.buildWantedInterceptMsg(order))
	if err != nil {
		// 顺丰业务错误作为正常响应返回，调用方直接读 success/errorCode/errorMsg
		if remote, ok := asSfRemoteError(err); ok {
			return &biz.SfInterceptResult{Success: sfSuccessFalse, ErrorCode: remote.Code, ErrorMsg: remote.Message}, nil
		}
		return nil, err
	}

	reply := &biz.SfInterceptResult{Success: sfSuccessTrue, ErrorCode: sfStdResultCodeSuccess}
	if err := r.decodeWantedInterceptMsgData(ctx, msgData, reply); err != nil {
		return nil, err
	}
	return reply, nil
}

// buildWantedInterceptMsg 组装截单转寄退回报文，月结卡号缺失时取配置，原单或本次为月结时必填
func (r *sfRepo) buildWantedInterceptMsg(order *biz.SfInterceptOrder) *sfWantedInterceptMsg {
	msg := &sfWantedInterceptMsg{
		WaybillNo:      order.WaybillNo,
		ServiceCode:    order.ServiceCode,
		Role:           order.Role,
		PayMode:        order.PayMode,
		MonthlyCardNo:  order.MonthlyCardNo,
		ProductType:    order.ProductType,
		CodAmount:      order.CodAmount,
		DeliverDate:    order.DeliverDate,
		DeliverTimeMin: order.DeliverTimeMin,
		DeliverTimeMax: order.DeliverTimeMax,
		SelfPickPoint:  order.SelfPickPoint,
		NewDestAddress: buildSfNewDestAddress(order.NewDestAddress),
		Cancel:         order.Cancel,
		CusID:          order.CusID,
	}
	// 月结卡号未传时取配置，原单为月结或本次改月结付款时需要
	if msg.MonthlyCardNo == "" {
		msg.MonthlyCardNo = r.conf.GetCustID()
	}
	return msg
}

// buildSfNewDestAddress 转换新目的地信息，nil 时返回 nil
func buildSfNewDestAddress(a *biz.SfNewDestAddress) *sfNewDestAddress {
	if a == nil {
		return nil
	}
	return &sfNewDestAddress{
		Country:      a.Country,
		CountryCode:  a.CountryCode,
		Province:     a.Province,
		City:         a.City,
		County:       a.County,
		Address:      a.Address,
		Company:      a.Company,
		Contact:      a.Contact,
		Phone:        a.Phone,
		AreaCode:     a.AreaCode,
		LocationCode: a.LocationCode,
	}
}

// ExpReceFilterOrderBsp 订单筛选，按地址判断订单是否可收派，支持一次传入多个订单
func (r *sfRepo) ExpReceFilterOrderBsp(ctx context.Context, orders *biz.SfFilterOrders) (*biz.SfFilterOrdersResult, error) {
	msgData, err := r.callStdService(ctx, sfServiceCodeFilterOrder, r.buildFilterOrderMsg(orders))
	if err != nil {
		// 顺丰业务错误作为正常响应返回，调用方直接读 success/errorCode/errorMsg
		if remote, ok := asSfRemoteError(err); ok {
			return &biz.SfFilterOrdersResult{Success: sfSuccessFalse, ErrorCode: remote.Code, ErrorMsg: remote.Message}, nil
		}
		return nil, err
	}

	var resp sfFilterOrderResp
	if err := r.decodeStdMsgData(ctx, wrapFilterOrderMsgData(msgData), &resp); err != nil {
		return nil, err
	}
	reply := toBizFilterOrdersResult(&resp)
	reply.Success = sfSuccessTrue
	reply.ErrorCode = sfStdResultCodeSuccess
	return reply, nil
}

// wrapFilterOrderMsgData 顺丰实际返回的 msgData 直接是结果数组，文档示例则为 {"resDtos":[...]}，
// 数组时包一层 resDtos 以兼容两种格式
func wrapFilterOrderMsgData(msgData json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(msgData)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return msgData
	}
	wrapped := make([]byte, 0, len(trimmed)+len(`{"resDtos":}`))
	wrapped = append(wrapped, `{"resDtos":`...)
	wrapped = append(wrapped, trimmed...)
	wrapped = append(wrapped, '}')
	return wrapped
}

// buildFilterOrderMsg 组装订单筛选报文，筛单类别缺省为自动筛单，月结卡号缺失时取配置
func (r *sfRepo) buildFilterOrderMsg(orders *biz.SfFilterOrders) []sfFilterOrderMsg {
	// 顺丰要求 msgData 为数组，即使只有一条也要以数组形式传递
	msgs := make([]sfFilterOrderMsg, 0, len(orders.Orders))
	for _, o := range orders.Orders {
		msg := sfFilterOrderMsg{
			FilterType:  o.FilterType,
			OrderID:     o.OrderID,
			MonthlyCard: o.MonthlyCard,
		}
		// 筛单类别未传时按自动筛单处理，顺丰默认值也是 1
		if msg.FilterType == 0 {
			msg.FilterType = sfFilterTypeAuto
		}
		if msg.MonthlyCard == "" {
			msg.MonthlyCard = r.conf.GetCustID()
		}
		for _, c := range o.ContactInfos {
			msg.ContactInfos = append(msg.ContactInfos, sfFilterAddrInfo{
				ContactType: c.ContactType,
				Tel:         c.Tel,
				Country:     c.Country,
				Province:    c.Province,
				City:        c.City,
				County:      c.County,
				Address:     c.Address,
				PostCode:    c.PostCode,
			})
		}
		msgs = append(msgs, msg)
	}
	return msgs
}

// decodeWantedInterceptMsgData 解析截单转寄退回业务结果，
// 顺丰部分环境会在 msgData 内再包一层 {success,errorCode,errorMsg,msgData}，此时以内层结果为准
func (r *sfRepo) decodeWantedInterceptMsgData(ctx context.Context, msgData json.RawMessage, reply *biz.SfInterceptResult) error {
	if len(msgData) == 0 || string(msgData) == "null" {
		return nil
	}

	var result sfWantedInterceptResult
	if err := json.Unmarshal(msgData, &result); err != nil {
		r.logger.ErrorContext(ctx, "decode sf wanted intercept msg data failed", "err", err, "msgData", string(msgData))
		return biz.ErrSfStdService.WithCause(err)
	}

	// 内层还有业务结果时取内层
	if len(result.MsgData) > 0 && string(result.MsgData) != "null" {
		var inner sfWantedInterceptResult
		if err := json.Unmarshal(result.MsgData, &inner); err != nil {
			r.logger.ErrorContext(ctx, "decode inner sf wanted intercept msg data failed", "err", err, "msgData", string(result.MsgData))
			return biz.ErrSfStdService.WithCause(err)
		}
		result = inner
	}

	// 内层明确返回失败时按业务失败返回，出错时顺丰不返回指令编码
	if result.ErrorCode != "" && result.ErrorCode != sfStdResultCodeSuccess {
		reply.Success = sfSuccessFalse
		reply.ErrorCode = result.ErrorCode
		reply.ErrorMsg = result.ErrorMsg
		return nil
	}

	reply.CusID = result.CusID
	reply.Amount = string(result.Amount)
	if result.Freight != nil {
		reply.Freight = &biz.SfFreightInfo{
			InterceptionDeptCode: result.Freight.InterceptionDeptCode,
			TransferFlg:          result.Freight.TransferFlg,
		}
	}
	return nil
}

// decodeStdMsgData 将顺丰内层业务报文解析到 PO，顺丰返回的字段可能多于协议定义，未知字段直接忽略
func (r *sfRepo) decodeStdMsgData(ctx context.Context, msgData json.RawMessage, out any) error {
	if len(msgData) == 0 || string(msgData) == "null" {
		return nil
	}
	if err := json.Unmarshal(msgData, out); err != nil {
		r.logger.ErrorContext(ctx, "decode sf msg data failed", "err", err, "msgData", string(msgData))
		return biz.ErrSfStdService.WithCause(err)
	}
	return nil
}

// callStdService 调用顺丰统一入口（OAuth2 令牌认证），返回内层业务报文
func (r *sfRepo) callStdService(ctx context.Context, serviceCode string, msgData interface{}) (json.RawMessage, error) {
	if r.conf == nil || r.conf.PartnerID == "" || r.conf.Host == "" {
		r.logger.ErrorContext(ctx, "sf open platform config is missing")
		return nil, biz.ErrSfConfigInvalid
	}

	// 统一入口使用访问令牌鉴权，令牌由 GetAccessToken 统一缓存并在过期前续期
	token, err := r.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}

	msgDataBytes, err := json.Marshal(msgData)
	if err != nil {
		r.logger.ErrorContext(ctx, "encode sf msg data failed", "serviceCode", serviceCode, "err", err)
		return nil, biz.ErrSfStdService.WithCause(err)
	}

	params := url.Values{}
	params.Set("partnerID", r.conf.PartnerID)
	params.Set("requestID", uuid.NewString())
	params.Set("serviceCode", serviceCode)
	params.Set("timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	params.Set("msgData", string(msgDataBytes))
	params.Set("accessToken", token.AccessToken)

	reqURL := strings.TrimRight(r.conf.Host, "/") + sfStdServicePath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(params.Encode()))
	if err != nil {
		r.logger.ErrorContext(ctx, "build sf std service request failed", "serviceCode", serviceCode, "err", err)
		return nil, biz.ErrSfStdService.WithCause(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")

	resp, err := r.cli.Do(req)
	if err != nil {
		r.logger.ErrorContext(ctx, "request sf std service failed", "serviceCode", serviceCode, "err", err)
		return nil, biz.ErrSfStdService.WithCause(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		r.logger.ErrorContext(ctx, "read sf std service response failed", "serviceCode", serviceCode, "err", err)
		return nil, biz.ErrSfStdService.WithCause(err)
	}

	var stdResp sfStdServiceResp
	if err := json.Unmarshal(body, &stdResp); err != nil {
		r.logger.ErrorContext(ctx, "decode sf std service response failed", "serviceCode", serviceCode, "err", err, "body", string(body))
		return nil, biz.ErrSfStdService.WithCause(err)
	}

	// 兼容新老两种响应格式，取出内层业务报文
	inner := stdResp
	if stdResp.ApiResultCode != "" {
		// 新格式：外层 A1000 仅代表网关受理，业务结果在内层报文
		if stdResp.ApiResultCode != sfResultCodeSuccess {
			r.logger.ErrorContext(ctx, "sf gateway rejected request", "serviceCode", serviceCode,
				"apiResultCode", stdResp.ApiResultCode, "apiErrorMsg", stdResp.ApiErrorMsg)
			return nil, &sfRemoteError{Code: stdResp.ApiResultCode, Message: stdResp.ApiErrorMsg}
		}
		inner = sfStdServiceResp{}
		if err := json.Unmarshal([]byte(stdResp.ApiResultData), &inner); err != nil {
			r.logger.ErrorContext(ctx, "decode inner sf std service response failed", "serviceCode", serviceCode,
				"err", err, "data", stdResp.ApiResultData)
			return nil, biz.ErrSfStdService.WithCause(err)
		}
	}

	// 业务报文 errorCode 为 S0000 才算处理成功
	if inner.ErrorCode != sfStdResultCodeSuccess {
		r.logger.ErrorContext(ctx, "sf business request failed", "serviceCode", serviceCode,
			"errorCode", inner.ErrorCode, "errorMsg", inner.ErrorMsg)
		return nil, &sfRemoteError{Code: inner.ErrorCode, Message: inner.ErrorMsg}
	}
	return inner.MsgData, nil
}

// sfRemoteError 顺丰网关返回的业务错误，由各业务接口转成响应字段返回给调用方
type sfRemoteError struct {
	Code    string // 顺丰错误码
	Message string // 顺丰错误描述
}

func (e *sfRemoteError) Error() string {
	return fmt.Sprintf("sf express returned error, errorCode: %s, errorMsg: %s", e.Code, e.Message)
}

// asSfRemoteError 判断错误是否来自顺丰网关，是则返回错误明细
func asSfRemoteError(err error) (*sfRemoteError, bool) {
	var remote *sfRemoteError
	if stderrors.As(err, &remote) {
		return remote, true
	}
	return nil, false
}

// toBizOrderSearchResult 订单结果查询 PO 转 DO
func toBizOrderSearchResult(po *sfOrderSearchResp) *biz.SfOrderSearchResult {
	return &biz.SfOrderSearchResult{
		OrderID:                po.OrderID,
		MainWaybillNo:          po.MainWaybillNo,
		WaybillNoInfos:         toBizWaybillNoInfos(po.WaybillNoInfoList),
		ResStatus:              po.ResStatus,
		OriginCode:             po.OriginCode,
		DestCode:               po.DestCode,
		FilterResult:           po.FilterResult,
		Remark:                 rawToAny(po.Remark),
		RouteLabelInfos:        toBizRouteLabelInfos(po.RouteLabelInfo),
		ContactInfo:            rawToAny(po.ContactInfo),
		ClientCode:             po.ClientCode,
		ServiceList:            rawToAny(po.ServiceList),
		ReturnExtraInfoList:    rawToAny(po.ReturnExtraInfoList),
		DeliverConsigneeRemark: po.DeliverConsigneeRemark,
		IsDelete:               po.IsDelete,
		BackWaybillNo:          po.BackWaybillNo,
		TotalQty:               po.TotalQty,
	}
}

// toBizDeliverTmResult 时效价格查询 PO 转 DO
func toBizDeliverTmResult(po *sfDeliverTmResp) *biz.SfDeliverTmResult {
	result := &biz.SfDeliverTmResult{
		DeliverTmDtos: make([]biz.SfDeliverTm, 0, len(po.DeliverTmDto)),
	}
	for _, d := range po.DeliverTmDto {
		result.DeliverTmDtos = append(result.DeliverTmDtos, biz.SfDeliverTm{
			BusinessType:     d.BusinessType,
			BusinessTypeDesc: d.BusinessTypeDesc,
			DeliverTime:      d.DeliverTime,
			SearchPrice:      d.SearchPrice,
			Fee:              d.Fee,
			CloseTime:        d.CloseTime,
		})
	}
	return result
}

// toBizCreateOrderResult 下单 PO 转 DO
func toBizCreateOrderResult(po *sfCreateOrderResp) *biz.SfCreateOrderResult {
	return &biz.SfCreateOrderResult{
		OrderID:                   po.OrderID,
		OriginCode:                po.OriginCode,
		DestCode:                  po.DestCode,
		FilterResult:              po.FilterResult,
		Remark:                    po.Remark,
		URL:                       po.URL,
		PaymentLink:               po.PaymentLink,
		IsUpstairs:                po.IsUpstairs,
		IsSpecialWarehouseService: po.IsSpecialWarehouseService,
		MappingMark:               po.MappingMark,
		AgentMailno:               po.AgentMailno,
		ReturnExtraInfoList:       rawToAny(po.ReturnExtraInfoList),
		WaybillNoInfos:            toBizWaybillNoInfos(po.WaybillNoInfoList),
		RouteLabelInfos:           toBizRouteLabelInfos(po.RouteLabelInfo),
		ContactInfoList:           rawToAny(po.ContactInfoList),
		SendStartTm:               po.SendStartTm,
		CustomerRights:            rawToAny(po.CustomerRights),
		ExpressTypeID:             rawToAny(po.ExpressTypeID),
	}
}

// toBizUpdateOrderResult 订单确认/取消 PO 转 DO
func toBizUpdateOrderResult(po *sfUpdateOrderResp) *biz.SfUpdateOrderResult {
	return &biz.SfUpdateOrderResult{
		OrderID:        po.OrderID,
		WaybillNoInfos: toBizWaybillNoInfos(po.WaybillNoInfoList),
		ResStatus:      po.ResStatus,
		ExtraInfoList:  rawToAny(po.ExtraInfoList),
	}
}

// toBizFilterOrdersResult 订单筛选 PO 转 DO
func toBizFilterOrdersResult(po *sfFilterOrderResp) *biz.SfFilterOrdersResult {
	result := &biz.SfFilterOrdersResult{
		Results: make([]biz.SfFilterResult, 0, len(po.ResDtos)),
	}
	for _, d := range po.ResDtos {
		result.Results = append(result.Results, biz.SfFilterResult{
			OrderID:      d.OrderID,
			FilterResult: d.FilterResult,
			OriginCode:   d.OriginCode,
			DestCode:     d.DestCode,
			Remark:       d.Remark,
		})
	}
	return result
}

// toBizWaybillNoInfos 运单号信息 PO 转 DO
func toBizWaybillNoInfos(pos []sfWaybillInfo) []biz.SfWaybillNoInfo {
	if len(pos) == 0 {
		return nil
	}
	infos := make([]biz.SfWaybillNoInfo, 0, len(pos))
	for _, po := range pos {
		infos = append(infos, biz.SfWaybillNoInfo{
			WaybillType: po.WaybillType,
			WaybillNo:   po.WaybillNo,
			BoxNo:       po.BoxNo,
			Length:      po.Length,
			Width:       po.Width,
			Height:      po.Height,
			Weight:      po.Weight,
			Volume:      po.Volume,
		})
	}
	return infos
}

// rawToAny 把顺丰透传报文的原始 JSON 转成调用方可直接序列化的值，空值与 null 返回 nil
func rawToAny(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return value
}

// toBizRouteLabelInfos 路由标签信息 PO 转 DO
func toBizRouteLabelInfos(pos []sfRouteLabelInfo) []biz.SfRouteLabelInfo {
	if len(pos) == 0 {
		return nil
	}
	infos := make([]biz.SfRouteLabelInfo, 0, len(pos))
	for _, po := range pos {
		infos = append(infos, biz.SfRouteLabelInfo{
			Code:       po.Code,
			RouteLabel: toBizRouteLabel(po.RouteLabel),
			Message:    po.Message,
		})
	}
	return infos
}

// toBizRouteLabel 路由标签数据 PO 转 DO，nil 时返回 nil
func toBizRouteLabel(po *sfRouteLabelData) *biz.SfRouteLabel {
	if po == nil {
		return nil
	}
	return &biz.SfRouteLabel{
		WaybillNo:              po.WaybillNo,
		SourceTransferCode:     po.SourceTransferCode,
		SourceCityCode:         po.SourceCityCode,
		SourceDeptCode:         po.SourceDeptCode,
		SourceTeamCode:         po.SourceTeamCode,
		DestCityCode:           po.DestCityCode,
		DestDeptCode:           po.DestDeptCode,
		DestDeptCodeMapping:    po.DestDeptCodeMapping,
		DestTeamCode:           po.DestTeamCode,
		DestTeamCodeMapping:    po.DestTeamCodeMapping,
		DestTransferCode:       po.DestTransferCode,
		DestRouteLabel:         po.DestRouteLabel,
		ProName:                po.ProName,
		ProCode:                po.ProCode,
		CargoTypeCode:          po.CargoTypeCode,
		LimitTypeCode:          po.LimitTypeCode,
		ExpressTypeCode:        po.ExpressTypeCode,
		CodingMapping:          po.CodingMapping,
		CodingMappingOut:       po.CodingMappingOut,
		XBFlag:                 po.XBFlag,
		PrintFlag:              po.PrintFlag,
		PrintIcon:              po.PrintIcon,
		TwoDimensionCode:       po.TwoDimensionCode,
		ABFlag:                 po.ABFlag,
		DestPortCode:           po.DestPortCode,
		DestCountry:            po.DestCountry,
		DestPostCode:           po.DestPostCode,
		GoodsValueTotal:        po.GoodsValueTotal,
		CurrencySymbol:         po.CurrencySymbol,
		CusBatch:               po.CusBatch,
		GoodsNumber:            po.GoodsNumber,
		ErrMsg:                 po.ErrMsg,
		CheckCode:              po.CheckCode,
		ProIcon:                po.ProIcon,
		FileIcon:               po.FileIcon,
		FbaIcon:                po.FbaIcon,
		IcsmIcon:               po.IcsmIcon,
		DestGisDeptCode:        po.DestGisDeptCode,
		NewIcon:                rawToAny(po.NewIcon),
		DestAddrKeyWord:        po.DestAddrKeyWord,
		SendAreaCode:           po.SendAreaCode,
		DestinationStationCode: po.DestinationStationCode,
		SxLabelDestCode:        po.SxLabelDestCode,
		SxDestTransferCode:     po.SxDestTransferCode,
		SxCompany:              po.SxCompany,
		NewABFlag:              po.NewABFlag,
		RongType:               rawToAny(po.RongType),
		WaybillIconList:        rawToAny(po.WaybillIconList),
	}
}
