package biz

import (
	"context"

	v1 "logisticsServer/api/sf/v1"

	"github.com/go-kratos/kratos/v3/errors"
)

// 顺丰开放平台调用错误，统一在 biz 声明，data 只负责把网关与缓存故障映射过来。
var (
	// ErrSfConfigInvalid 配置缺失，无法调用顺丰网关
	ErrSfConfigInvalid = errors.InternalServer(v1.ErrorReason_SF_CONFIG_INVALID.String(), "顺丰开放平台配置缺失")
	// ErrSfTokenRequest 申请访问令牌失败
	ErrSfTokenRequest = errors.InternalServer(v1.ErrorReason_SF_TOKEN_REQUEST_FAILED.String(), "请求顺丰访问令牌失败")
	// ErrSfAuthFailed 顺丰鉴权失败
	ErrSfAuthFailed = errors.InternalServer(v1.ErrorReason_SF_AUTH_FAILED.String(), "顺丰鉴权失败")
	// ErrSfTokenEmpty 顺丰返回的访问令牌为空
	ErrSfTokenEmpty = errors.InternalServer(v1.ErrorReason_SF_TOKEN_EMPTY.String(), "顺丰返回的访问令牌为空")
	// ErrSfCacheFailed 访问令牌缓存读写失败
	ErrSfCacheFailed = errors.InternalServer(v1.ErrorReason_SF_CACHE_FAILED.String(), "顺丰访问令牌缓存读写失败")
	// ErrSfStdService 顺丰统一入口调用失败
	ErrSfStdService = errors.InternalServer(v1.ErrorReason_SF_STD_SERVICE_FAILED.String(), "调用顺丰统一入口失败")

	// ErrSfOrderSearchParam 订单结果查询参数缺失
	ErrSfOrderSearchParam = errors.BadRequest(v1.ErrorReason_SF_ORDER_SEARCH_PARAM_INVALID.String(), "客户订单号不能为空")
	// ErrSfDeliverTmParam 时效标准及价格查询参数缺失
	ErrSfDeliverTmParam = errors.BadRequest(v1.ErrorReason_SF_DELIVER_TM_PARAM_INVALID.String(), "寄件与收件地址需为省市区或区域代码，重量传入时必须大于 0")
	// ErrSfCreateOrderParam 下单参数缺失
	ErrSfCreateOrderParam = errors.BadRequest(v1.ErrorReason_SF_CREATE_ORDER_PARAM_INVALID.String(), "订单号、货物信息与收件人不能为空")
	// ErrSfUpdateOrderParam 订单确认/取消参数缺失
	ErrSfUpdateOrderParam = errors.BadRequest(v1.ErrorReason_SF_UPDATE_ORDER_PARAM_INVALID.String(), "订单号与处理类型不能为空，处理类型只能为1确认或2取消")
	// ErrSfWantedInterceptParam 截单转寄退回参数缺失
	ErrSfWantedInterceptParam = errors.BadRequest(v1.ErrorReason_SF_WANTED_INTERCEPT_PARAM_INVALID.String(), "运单号、指令类型、发起方与付款方式不能为空，取消转寄/退回指令时需传 cusId")
	// ErrSfFilterOrderParam 订单筛选参数缺失
	ErrSfFilterOrderParam = errors.BadRequest(v1.ErrorReason_SF_FILTER_ORDER_BSP_PARAM_INVALID.String(), "筛选订单不能为空，筛单类别只能为1自动筛单或2可人工筛单")
)

// 筛单类别
const (
	SfFilterTypeAuto   = 1 // 自动筛单，系统根据地址库判断并返回结果
	SfFilterTypeManual = 2 // 可人工筛单，无法自动判断时转为人工处理
)

// 收寄双方联系人类型
const (
	SfContactTypeSender    = 1 // 寄件人
	SfContactTypeConsignee = 2 // 收件人
)

// 订单确认/取消的处理类型
const (
	SfDealTypeConfirm = 1 // 确认订单
	SfDealTypeCancel  = 2 // 取消订单
)

// 截单转寄退回的指令类型
const (
	SfInterceptServiceCodeTransfer = "1" // 转寄
	SfInterceptServiceCodeReturn   = "2" // 退回
)

// 截单转寄退回的枚举取值，非法值会被顺丰网关拒绝，提前拦截
var (
	sfInterceptServiceCodes = map[string]struct{}{
		"1": {}, "2": {}, "3": {}, "4": {}, "6": {},
		"7": {}, "8": {}, "9": {}, "10": {}, "12": {}, "38": {},
	}
	sfInterceptRoles    = map[string]struct{}{"1": {}, "2": {}}                   // 1寄方 2 收方
	sfInterceptPayModes = map[string]struct{}{"1": {}, "2": {}, "3": {}, "4": {}} // 1寄付现结 2到付现结 3寄付转第三方月结 4寄付月结
)

// SfToken 顺丰访问令牌
type SfToken struct {
	AccessToken string // 访问令牌
	ExpiresIn   int32  // 剩余有效期，单位秒
}

// SfConfig 顺丰调用配置，不含校验码等密钥信息
type SfConfig struct {
	Token        *SfToken // 访问令牌
	PartnerID    string   // 顾客编码
	MonthlyCard  string   // 月结卡号
	TemplateCode string   // 云打印面单模板编码
}

// SfRepo 顺丰开放平台数据访问接口。顺丰报文本身即领域模型，DO 在 biz 定义，
// data 负责与顺丰网关的 JSON 报文（PO）互转。
type SfRepo interface {
	// GetAccessToken 获取顺丰访问令牌，优先取缓存，缓存失效后重新申请
	GetAccessToken(context.Context) (*SfToken, error)
	// GetConfig 获取顺丰调用配置，包含访问令牌、顾客编码、月结卡号与面单模板编码
	GetConfig(context.Context) (*SfConfig, error)
	// ExpReceSearchOrderResp 订单结果查询，用于未收到下单返回数据时查询客户订单当前的处理状态
	ExpReceSearchOrderResp(context.Context, *SfOrderSearchQuery) (*SfOrderSearchResult, error)
	// ExpReceQueryDeliverTm 时效标准及价格查询，返回预计送达时间与运费
	ExpReceQueryDeliverTm(context.Context, *SfDeliverTmQuery) (*SfDeliverTmResult, error)
	// ExpReceCreateOrder 下单，支持月结卡号与代收货款等增值服务
	ExpReceCreateOrder(context.Context, *SfCreateOrder) (*SfCreateOrderResult, error)
	// ExpReceUpdateOrder 订单确认/取消，dealType 1确认 2取消
	ExpReceUpdateOrder(context.Context, *SfUpdateOrder) (*SfUpdateOrderResult, error)
	// ExpReceWantedIntercept 截单转寄退回，对在途运单下发转寄、退回、改派等拦截指令
	ExpReceWantedIntercept(context.Context, *SfInterceptOrder) (*SfInterceptResult, error)
	// ExpReceFilterOrderBsp 订单筛选，按地址判断订单是否可收派
	ExpReceFilterOrderBsp(context.Context, *SfFilterOrders) (*SfFilterOrdersResult, error)
}

// SfOrderSearchQuery 订单结果查询条件（EXP_RECE_SEARCH_ORDER_RESP）
type SfOrderSearchQuery struct {
	OrderID       string // 客户订单号
	SearchType    string // 查询类型：1正向单 2退货单
	Language      string // 响应报文语言
	MainWaybillNo string // 顺丰运单号（母单号）
}

// SfOrderSearchResult 订单结果查询结果
type SfOrderSearchResult struct {
	OrderID                string             // 客户订单号
	MainWaybillNo          string             // 顺丰运单号（母单号）
	WaybillNoInfos         []SfWaybillNoInfo  // 运单号信息列表
	ResStatus              int32              // 订单状态：0下单失败 1已取消 2已接单 3处理中 4派件中 5已签收 50全部
	OriginCode             string             // 原寄地代码
	DestCode               string             // 目的地代码
	FilterResult           string             // 筛单结果：0人工确认 1确认 2不确定
	Remark                 any                // 备注，透传顺丰原始报文
	RouteLabelInfos        []SfRouteLabelInfo // 路由标签信息（面单数据）
	ContactInfo            any                // 联系方式信息，透传顺丰原始报文
	ClientCode             string             // 客户端编码
	ServiceList            any                // 服务列表，透传顺丰原始报文
	ReturnExtraInfoList    any                // 返回扩展信息列表，透传顺丰原始报文
	DeliverConsigneeRemark string             // 派送备注
	IsDelete               string             // 订单是否删除
	BackWaybillNo          string             // 退货单运单号
	TotalQty               int32              // 总件数
	Success                string             // 顺丰是否处理成功："true"/"false"
	ErrorCode              string             // 顺丰响应码，S0000 表示成功
	ErrorMsg               string             // 顺丰错误描述
}

// SfWaybillNoInfo 运单号信息
type SfWaybillNoInfo struct {
	WaybillType int32   // 运单号类型：1母单 2子单 3退回件
	WaybillNo   string  // 运单号
	BoxNo       string  // 箱号，下单返回
	Length      float64 // 长，单位cm，下单返回
	Width       float64 // 宽，单位cm，下单返回
	Height      float64 // 高，单位cm，下单返回
	Weight      float64 // 重量，单位kg，下单返回
	Volume      float64 // 体积，单位cm³，下单返回
}

// SfRouteLabelInfo 路由标签信息
type SfRouteLabelInfo struct {
	Code       string        // 返回码，1000表示成功
	RouteLabel *SfRouteLabel // 路由标签数据（面单数据）
	Message    string        // 返回信息
}

// SfRouteLabel 路由标签数据
type SfRouteLabel struct {
	WaybillNo              string // 运单号
	SourceTransferCode     string // 原寄地中转场代码
	SourceCityCode         string // 原寄地城市代码
	SourceDeptCode         string // 原寄地网点代码
	SourceTeamCode         string // 原寄地分部代码
	DestCityCode           string // 目的地城市代码
	DestDeptCode           string // 目的地网点代码
	DestDeptCodeMapping    string // 目的地网点代码映射
	DestTeamCode           string // 目的地分部代码
	DestTeamCodeMapping    string // 目的地分部代码映射
	DestTransferCode       string // 目的地中转场代码
	DestRouteLabel         string // 目的地路由标签
	ProName                string // 产品名称
	ProCode                string // 产品编码
	CargoTypeCode          string // 货物类型编码
	LimitTypeCode          string // 限制类型编码
	ExpressTypeCode        string // 快递类型编码
	CodingMapping          string // 编码映射
	CodingMappingOut       string // 外部编码映射
	XBFlag                 string // XB标识
	PrintFlag              string // 打印标识
	PrintIcon              string // 打印图标
	TwoDimensionCode       string // 二维码内容
	ABFlag                 string // AB标识
	DestPortCode           string // 目的地港口代码
	DestCountry            string // 目的地国家
	DestPostCode           string // 目的地邮编
	GoodsValueTotal        string // 货物总价值
	CurrencySymbol         string // 货币符号
	CusBatch               string // 客户批次
	GoodsNumber            string // 货物数量
	ErrMsg                 string // 错误信息
	CheckCode              string // 校验码
	ProIcon                string // 产品图标
	FileIcon               string // 文件图标
	FbaIcon                string // FBA图标
	IcsmIcon               string // ICSM图标
	DestGisDeptCode        string // 目的地GIS网点代码
	NewIcon                any    // 新图标，透传顺丰原始报文
	DestAddrKeyWord        string // 目的地地址关键字
	SendAreaCode           string // 寄件区域代码
	DestinationStationCode string // 目的站点代码
	SxLabelDestCode        string // 时效标签目的地代码
	SxDestTransferCode     string // 时效标签目的地中转场代码
	SxCompany              string // 时效标签公司
	NewABFlag              string // 新AB标识
	RongType               any    // 融通类型，透传顺丰原始报文
	WaybillIconList        any    // 运单图标列表，透传顺丰原始报文
}

// SfAddress 寄收地址信息，Code 与省市区二选一填写
type SfAddress struct {
	Province string // 省份
	City     string // 城市
	District string // 区/县
	Address  string // 详细地址
	Code     string // 地区域代码，填写本项后会忽略省市县具体地址
}

// SfDeliverTmQuery 时效标准及价格查询条件（EXP_RECE_QUERY_DELIVERTM）
type SfDeliverTmQuery struct {
	BusinessType  string     // 时效代码
	ConsignedTime string     // 寄件时间
	SrcAddress    *SfAddress // 原寄地信息
	DestAddress   *SfAddress // 目的地信息
	SearchPrice   string     // 是否查询价格：1查询 0不查询
	Weight        *float64   // 货物总重量，单位kg
	Volume        float64    // 货物体积
	MonthlyCard   string     // 月结卡号，为空时取配置
}

// SfDeliverTmResult 时效标准及价格查询结果
type SfDeliverTmResult struct {
	DeliverTmDtos []SfDeliverTm // 时效与价格信息列表
	Success       string        // 顺丰是否处理成功："true"/"false"
	ErrorCode     string        // 顺丰响应码，S0000 表示成功
	ErrorMsg      string        // 顺丰错误描述
}

// SfDeliverTm 时效与价格信息
type SfDeliverTm struct {
	BusinessType     string  // 快件产品（时效代码）
	BusinessTypeDesc string  // 快件产品描述
	DeliverTime      string  // 承诺时间
	SearchPrice      string  // 是否查询价格：1返回价格 0不返回价格
	Fee              float64 // 价格，单位元
	CloseTime        string  // 截单时间
}

// SfCreateOrder 下单条件（EXP_RECE_CREATE_ORDER）
type SfCreateOrder struct {
	OrderID      string          // 客户订单号
	MonthlyCard  string          // 月结卡号，为空时取配置
	Language     string          // 语言，缺省 zh_CN
	ContactInfos []SfContactInfo // 收寄双方信息，无寄件人时取配置
	CargoDetails []SfCargoDetail // 货物信息
	ServiceList  []SfServiceItem // 增值服务
}

// SfContactInfo 收寄双方信息
type SfContactInfo struct {
	ContactType int32  // 1寄件人 2收件人
	Contact     string // 联系人姓名
	Company     string // 公司名称
	Tel         string // 联系电话
	Country     string // 国家代码
	PostCode    string // 邮编
	Address     string // 详细地址
}

// SfCargoDetail 货物信息
type SfCargoDetail struct {
	Name       string  // 货物名称
	Count      float64 // 货物数量
	Unit       string  // 货物单位
	Weight     float64 // 货物重量，单位kg
	Amount     float64 // 货物金额
	Currency   string  // 币种
	SourceArea string  // 原产地
}

// SfServiceItem 增值服务
type SfServiceItem struct {
	Name   string // 增值服务名称
	Value  string // 增值服务值
	Value1 string // 附加属性1
	Value2 string // 附加属性2
}

// SfCreateOrderResult 下单结果
type SfCreateOrderResult struct {
	OrderID                   string             // 客户订单号
	OriginCode                string             // 原寄地代码
	DestCode                  string             // 目的地代码
	FilterResult              int32              // 筛单结果
	Remark                    string             // 备注
	URL                       string             // 面单打印地址
	PaymentLink               string             // 支付链接
	IsUpstairs                bool               // 是否上楼
	IsSpecialWarehouseService bool               // 是否特殊仓服务
	MappingMark               string             // 映射标记
	AgentMailno               string             // 代理运单号
	ReturnExtraInfoList       any                // 返回扩展信息列表，透传顺丰原始报文
	WaybillNoInfos            []SfWaybillNoInfo  // 运单号信息列表
	RouteLabelInfos           []SfRouteLabelInfo // 路由标签信息（面单数据）
	ContactInfoList           any                // 顺丰返回的收寄双方信息，透传顺丰原始报文
	SendStartTm               string             // 发货时间
	CustomerRights            any                // 客户权益，透传顺丰原始报文
	ExpressTypeID             any                // 产品类型ID，透传顺丰原始报文
	Success                   string             // 顺丰是否处理成功："true"/"false"
	ErrorCode                 string             // 顺丰响应码，S0000 表示成功
	ErrorMsg                  string             // 顺丰错误描述
}

// SfUpdateOrder 订单确认/取消条件（EXP_RECE_UPDATE_ORDER）
type SfUpdateOrder struct {
	OrderID        string
	DealType       int32             // 1确认 2取消
	Language       string            // 语言，缺省 zh-CN
	TotalWeight    float64           // 订单总重量，确认时必填
	WaybillNoInfos []SfWaybillNoInfo // 顺丰运单号信息，取消时可为空
}

// SfUpdateOrderResult 订单确认/取消结果
type SfUpdateOrderResult struct {
	OrderID        string            // 客户订单号
	WaybillNoInfos []SfWaybillNoInfo // 顺丰运单号信息
	ResStatus      int32             // 结果状态：1客户订单号与顺丰运单号不匹配 2操作成功
	ExtraInfoList  any               // 扩展属性，透传顺丰原始报文
	Success        string            // 顺丰是否处理成功："true"/"false"
	ErrorCode      string            // 顺丰响应码，S0000 表示成功
	ErrorMsg       string            // 顺丰错误描述
}

// SfInterceptOrder 截单转寄退回条件（EXP_RECE_WANTED_INTERCEPT）
type SfInterceptOrder struct {
	WaybillNo      string            // 运单号
	ServiceCode    string            // 指令类型：1转寄 2退回 等
	Role           string            // 发起方：1寄方 2收方
	PayMode        string            // 付款方式：1寄付现结 2到付现结 3寄付转第三方月结 4寄付月结
	MonthlyCardNo  string            // 月结卡号，为空时取配置
	ProductType    string            // 产品类型
	CodAmount      *float64          // 代收货款金额，修改代收货款时传入
	DeliverDate    string            // 派送日期，格式 yyyy-MM-dd
	DeliverTimeMin string            // 派送最早时间，格式 HH:mm
	DeliverTimeMax string            // 派送最晚时间，格式 HH:mm
	SelfPickPoint  string            // 自取点：1合作点 2快递柜 3网点 4中转场
	NewDestAddress *SfNewDestAddress // 新目的地信息，取消指令时不传
	Cancel         bool              // true 取消转寄/退回指令，false 下发指令
	CusID          string            // 指令编码，取消时必填
}

// SfNewDestAddress 新目的地信息，转寄/退回必须携带地址、联系人姓名与电话
type SfNewDestAddress struct {
	Country      string // 国家
	CountryCode  string // 国家编码
	Province     string // 省
	City         string // 市
	County       string // 区
	Address      string // 详细地址，不含省市区
	Company      string // 公司名称
	Contact      string // 联系人姓名
	Phone        string // 联系电话
	AreaCode     string // 网点代码
	LocationCode string // 城市代码
}

// SfInterceptResult 截单转寄退回结果
type SfInterceptResult struct {
	CusID     string         // 指令编码，取消指令时回传
	Amount    string         // 费用参数
	Freight   *SfFreightInfo // 费用附加信息
	Success   string         // 顺丰是否处理成功："true"/"false"
	ErrorCode string         // 顺丰响应码，S0000 表示成功
	ErrorMsg  string         // 顺丰错误描述
}

// SfFreightInfo 费用附加信息
type SfFreightInfo struct {
	InterceptionDeptCode string // 预估拦截地
	TransferFlg          string // 拦截地是否为中转场：1中转场，其余值非中转场
}

// SfFilterOrders 订单筛选条件（EXP_RECE_FILTER_ORDER_BSP）
type SfFilterOrders struct {
	Orders []SfFilterOrder // 待筛选订单列表
}

// SfFilterOrder 订单筛选条件
type SfFilterOrder struct {
	FilterType   int32          // 筛单类别：1自动筛单 2可人工筛单
	OrderID      string         // 客户订单号
	MonthlyCard  string         // 月结卡号，为空时取配置
	ContactInfos []SfFilterAddr // 地址信息
}

// SfFilterAddr 筛选地址信息
type SfFilterAddr struct {
	ContactType int32  // 地址类型：1寄件方 2到件方
	Tel         string // 联系电话
	Country     string // 国家或地区代码
	Province    string // 省级行政区名称
	City        string // 地级行政区名称
	County      string // 县/区级行政区名称
	Address     string // 详细地址
	PostCode    string // 邮编
}

// SfFilterOrdersResult 订单筛选结果
type SfFilterOrdersResult struct {
	Results   []SfFilterResult // 筛选结果列表，顺序与请求一致
	Success   string           // 顺丰是否处理成功："true"/"false"
	ErrorCode string           // 顺丰响应码，S0000 表示成功
	ErrorMsg  string           // 顺丰错误描述
}

// SfFilterResult 单个订单的筛选结果
type SfFilterResult struct {
	OrderID      string // 客户订单号
	FilterResult int32  // 筛选结果：1人工确认 2可收派 3不可以收派 4地址无法确认
	OriginCode   string // 原寄地区域代码，可收派时不为空
	DestCode     string // 目的地区域代码，可收派时不为空
	Remark       string // 不可以收派的原因代码
}

// SfUsecase 顺丰开放平台用例，负责参数校验并把请求转交数据层
type SfUsecase struct {
	repo SfRepo
}

// NewSfUsecase new a Sf usecase.
func NewSfUsecase(repo SfRepo) *SfUsecase {
	return &SfUsecase{repo: repo}
}

// GetAccessToken 获取顺丰访问令牌，令牌由服务端统一缓存并自动续期
func (uc *SfUsecase) GetAccessToken(ctx context.Context) (*SfToken, error) {
	return uc.repo.GetAccessToken(ctx)
}

// GetConfig 获取顺丰调用配置，访问令牌取自统一缓存
func (uc *SfUsecase) GetConfig(ctx context.Context) (*SfConfig, error) {
	return uc.repo.GetConfig(ctx)
}

// ExpReceSearchOrderResp 订单结果查询，用于未收到下单返回数据时查询客户订单当前的处理状态
func (uc *SfUsecase) ExpReceSearchOrderResp(ctx context.Context, query *SfOrderSearchQuery) (*SfOrderSearchResult, error) {
	// 客户订单号必传，运单号为条件字段可不传
	if query == nil || query.OrderID == "" {
		return nil, ErrSfOrderSearchParam
	}
	return uc.repo.ExpReceSearchOrderResp(ctx, query)
}

// ExpReceQueryDeliverTm 时效标准及价格查询，根据寄收地址、重量与业务类型查询预计送达时间与运费
func (uc *SfUsecase) ExpReceQueryDeliverTm(ctx context.Context, query *SfDeliverTmQuery) (*SfDeliverTmResult, error) {
	// 顺丰要求寄件与收件地址必传，地址支持省市区或区域代码两种写法
	if query == nil || !validSfAddress(query.SrcAddress) || !validSfAddress(query.DestAddress) {
		return nil, ErrSfDeliverTmParam
	}
	// 重量为非必传字段，仅当调用方传了值时才要求大于 0
	if query.Weight != nil && *query.Weight <= 0 {
		return nil, ErrSfDeliverTmParam
	}
	return uc.repo.ExpReceQueryDeliverTm(ctx, query)
}

// validSfAddress 地区域代码与省市区至少填一组，按代码查询时忽略省市县具体地址
func validSfAddress(a *SfAddress) bool {
	if a == nil {
		return false
	}
	if a.Code != "" {
		return true
	}
	return a.Province != "" && a.City != ""
}

// ExpReceCreateOrder 下单，支持月结卡号与代收货款等增值服务
func (uc *SfUsecase) ExpReceCreateOrder(ctx context.Context, order *SfCreateOrder) (*SfCreateOrderResult, error) {
	if order == nil || order.OrderID == "" || len(order.CargoDetails) == 0 {
		return nil, ErrSfCreateOrderParam
	}
	// 收件人必传，寄件人缺失时由 data 层用配置的默认寄件人补齐
	var hasConsignee bool
	for _, c := range order.ContactInfos {
		if c.ContactType == SfContactTypeConsignee {
			hasConsignee = true
			break
		}
	}
	if !hasConsignee {
		return nil, ErrSfCreateOrderParam
	}
	return uc.repo.ExpReceCreateOrder(ctx, order)
}

// ExpReceUpdateOrder 订单确认/取消，dealType 1确认 2取消
func (uc *SfUsecase) ExpReceUpdateOrder(ctx context.Context, order *SfUpdateOrder) (*SfUpdateOrderResult, error) {
	if order == nil || order.OrderID == "" {
		return nil, ErrSfUpdateOrderParam
	}
	// 顺丰只接受两种处理类型，传其他值会被网关拒绝，提前拦截
	if order.DealType != SfDealTypeConfirm && order.DealType != SfDealTypeCancel {
		return nil, ErrSfUpdateOrderParam
	}
	return uc.repo.ExpReceUpdateOrder(ctx, order)
}

// ExpReceWantedIntercept 截单转寄退回，对在途运单下发转寄、退回、改派等拦截指令，派件出仓前有效
func (uc *SfUsecase) ExpReceWantedIntercept(ctx context.Context, order *SfInterceptOrder) (*SfInterceptResult, error) {
	if order == nil || order.WaybillNo == "" {
		return nil, ErrSfWantedInterceptParam
	}
	// 指令类型、发起方与付款方式都是必传枚举，传其他值会被网关拒绝
	if !validSfInterceptEnum(sfInterceptServiceCodes, order.ServiceCode) ||
		!validSfInterceptEnum(sfInterceptRoles, order.Role) ||
		!validSfInterceptEnum(sfInterceptPayModes, order.PayMode) {
		return nil, ErrSfWantedInterceptParam
	}

	if order.Cancel {
		// 只支持取消转寄/退回指令，且必须带上指令下发时返回的指令编码
		if order.ServiceCode != SfInterceptServiceCodeTransfer && order.ServiceCode != SfInterceptServiceCodeReturn {
			return nil, ErrSfWantedInterceptParam
		}
		if order.CusID == "" {
			return nil, ErrSfWantedInterceptParam
		}
	}
	// 转寄/退回需要变更收件地址时由调用方传 newDestAddress，是否齐备由顺丰校验并返回具体原因
	return uc.repo.ExpReceWantedIntercept(ctx, order)
}

// validSfInterceptEnum 判断枚举取值是否受支持
func validSfInterceptEnum(options map[string]struct{}, value string) bool {
	_, ok := options[value]
	return ok
}

// ExpReceFilterOrderBsp 订单筛选，按地址判断订单是否可收派，支持一次传入多个订单
func (uc *SfUsecase) ExpReceFilterOrderBsp(ctx context.Context, orders *SfFilterOrders) (*SfFilterOrdersResult, error) {
	if orders == nil || len(orders.Orders) == 0 {
		return nil, ErrSfFilterOrderParam
	}
	// 仅筛单类别为必填，订单号、月结卡号与地址信息均为非必填，交由顺丰校验
	for _, o := range orders.Orders {
		// 筛单类别不传时由 data 层缺省为自动筛单，传其他值会被网关拒绝
		if o.FilterType != 0 && o.FilterType != SfFilterTypeAuto && o.FilterType != SfFilterTypeManual {
			return nil, ErrSfFilterOrderParam
		}
	}
	return uc.repo.ExpReceFilterOrderBsp(ctx, orders)
}
