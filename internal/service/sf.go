package service

import (
	"context"
	"fmt"
	"log/slog"

	v1 "logisticsServer/api/sf/v1"
	"logisticsServer/internal/biz"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// sfLogMaxLen 单条请求/响应日志的最大字符数，面单类报文较大，超长截断避免刷爆日志
const sfLogMaxLen = 2000

// SfService 顺丰开放平台接口。本层只做 DTO 与 DO 的转换，
// 参数校验与业务规则留在 biz，网关调用与报文转换留在 data。
type SfService struct {
	v1.UnimplementedSfServer

	uc     *biz.SfUsecase
	logger *slog.Logger
}

// NewSfService new a Sf service.
func NewSfService(uc *biz.SfUsecase, logger *slog.Logger) *SfService {
	return &SfService{uc: uc, logger: logger}
}

// GetAccessToken 获取顺丰访问令牌，请求无参数，仅记录返回参数
func (s *SfService) GetAccessToken(ctx context.Context, req *v1.GetAccessTokenRequest) (*v1.GetAccessTokenReply, error) {
	token, err := s.uc.GetAccessToken(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "SfService.GetAccessToken failed", "err", err)
		return nil, err
	}
	reply := &v1.GetAccessTokenReply{
		AccessToken: token.AccessToken,
		ExpiresIn:   token.ExpiresIn,
	}
	s.logger.InfoContext(ctx, "SfService.GetAccessToken succeeded",
		"accessToken", maskSfToken(reply.GetAccessToken()), "expiresIn", reply.GetExpiresIn())
	return reply, nil
}

// GetConfig 获取顺丰调用配置，请求无参数，仅记录返回参数且令牌脱敏
func (s *SfService) GetConfig(ctx context.Context, req *v1.GetConfigRequest) (*v1.GetConfigReply, error) {
	config, err := s.uc.GetConfig(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "SfService.GetConfig failed", "err", err)
		return nil, err
	}
	token := config.Token
	if token == nil {
		token = &biz.SfToken{}
	}
	reply := &v1.GetConfigReply{
		AccessToken:  token.AccessToken,
		ExpiresIn:    token.ExpiresIn,
		PartnerID:    config.PartnerID,
		MonthlyCard:  config.MonthlyCard,
		TemplateCode: config.TemplateCode,
	}
	s.logger.InfoContext(ctx, "SfService.GetConfig succeeded",
		"accessToken", maskSfToken(reply.GetAccessToken()), "expiresIn", reply.GetExpiresIn(),
		"partnerID", reply.GetPartnerID(), "monthlyCard", reply.GetMonthlyCard(), "templateCode", reply.GetTemplateCode())
	return reply, nil
}

// ExpReceSearchOrderResp 订单结果查询，未收到下单返回数据时查询客户订单当前的处理状态
func (s *SfService) ExpReceSearchOrderResp(ctx context.Context, req *v1.ExpReceSearchOrderRespRequest) (*v1.ExpReceSearchOrderRespReply, error) {
	s.logger.InfoContext(ctx, "SfService.ExpReceSearchOrderResp request", "req", sfLogJSON(req))
	result, err := s.uc.ExpReceSearchOrderResp(ctx, convertSfOrderSearchQuery(req))
	if err != nil {
		s.logger.ErrorContext(ctx, "SfService.ExpReceSearchOrderResp failed", "err", err)
		return nil, err
	}
	reply := convertSfOrderSearchReply(result)
	s.logger.InfoContext(ctx, "SfService.ExpReceSearchOrderResp succeeded", "reply", sfLogJSON(reply))
	return reply, nil
}

// ExpReceQueryDeliverTm 时效标准及价格查询，根据寄收地址、重量与业务类型查询预计送达时间与运费
func (s *SfService) ExpReceQueryDeliverTm(ctx context.Context, req *v1.ExpReceQueryDeliverTmRequest) (*v1.ExpReceQueryDeliverTmReply, error) {
	s.logger.InfoContext(ctx, "SfService.ExpReceQueryDeliverTm request", "req", sfLogJSON(req))
	result, err := s.uc.ExpReceQueryDeliverTm(ctx, convertSfDeliverTmQuery(req))
	if err != nil {
		s.logger.ErrorContext(ctx, "SfService.ExpReceQueryDeliverTm failed", "err", err)
		return nil, err
	}
	reply := convertSfDeliverTmReply(result)
	s.logger.InfoContext(ctx, "SfService.ExpReceQueryDeliverTm succeeded", "reply", sfLogJSON(reply))
	return reply, nil
}

// ExpReceCreateOrder 下单，支持月结卡号与代收货款等增值服务
func (s *SfService) ExpReceCreateOrder(ctx context.Context, req *v1.ExpReceCreateOrderRequest) (*v1.ExpReceCreateOrderReply, error) {
	s.logger.InfoContext(ctx, "SfService.ExpReceCreateOrder request", "req", sfLogJSON(req))
	result, err := s.uc.ExpReceCreateOrder(ctx, convertSfCreateOrder(req))
	if err != nil {
		s.logger.ErrorContext(ctx, "SfService.ExpReceCreateOrder failed", "err", err)
		return nil, err
	}
	reply := convertSfCreateOrderReply(result)
	s.logger.InfoContext(ctx, "SfService.ExpReceCreateOrder succeeded", "reply", sfLogJSON(reply))
	return reply, nil
}

// ExpReceUpdateOrder 订单确认/取消，dealType 1确认 2取消
func (s *SfService) ExpReceUpdateOrder(ctx context.Context, req *v1.ExpReceUpdateOrderRequest) (*v1.ExpReceUpdateOrderReply, error) {
	s.logger.InfoContext(ctx, "SfService.ExpReceUpdateOrder request", "req", sfLogJSON(req))
	result, err := s.uc.ExpReceUpdateOrder(ctx, convertSfUpdateOrder(req))
	if err != nil {
		s.logger.ErrorContext(ctx, "SfService.ExpReceUpdateOrder failed", "err", err)
		return nil, err
	}
	reply := convertSfUpdateOrderReply(result)
	s.logger.InfoContext(ctx, "SfService.ExpReceUpdateOrder succeeded", "reply", sfLogJSON(reply))
	return reply, nil
}

// ExpReceWantedIntercept 截单转寄退回，对在途运单下发转寄、退回、改派等拦截指令
func (s *SfService) ExpReceWantedIntercept(ctx context.Context, req *v1.ExpReceWantedInterceptRequest) (*v1.ExpReceWantedInterceptReply, error) {
	s.logger.InfoContext(ctx, "SfService.ExpReceWantedIntercept request", "req", sfLogJSON(req))
	result, err := s.uc.ExpReceWantedIntercept(ctx, convertSfInterceptOrder(req))
	if err != nil {
		s.logger.ErrorContext(ctx, "SfService.ExpReceWantedIntercept failed", "err", err)
		return nil, err
	}
	reply := convertSfInterceptReply(result)
	s.logger.InfoContext(ctx, "SfService.ExpReceWantedIntercept succeeded", "reply", sfLogJSON(reply))
	return reply, nil
}

// ExpReceFilterOrderBsp 订单筛选，按地址判断订单是否可收派，支持批量筛选
func (s *SfService) ExpReceFilterOrderBsp(ctx context.Context, req *v1.ExpReceFilterOrderBspRequest) (*v1.ExpReceFilterOrderBspReply, error) {
	s.logger.InfoContext(ctx, "SfService.ExpReceFilterOrderBsp request", "req", sfLogJSON(req))
	result, err := s.uc.ExpReceFilterOrderBsp(ctx, convertSfFilterOrders(req))
	if err != nil {
		s.logger.ErrorContext(ctx, "SfService.ExpReceFilterOrderBsp failed", "err", err)
		return nil, err
	}
	reply := convertSfFilterOrdersReply(result)
	s.logger.InfoContext(ctx, "SfService.ExpReceFilterOrderBsp succeeded", "reply", sfLogJSON(reply))
	return reply, nil
}

// convertSfOrderSearchQuery 订单结果查询请求转 DO
func convertSfOrderSearchQuery(in *v1.ExpReceSearchOrderRespRequest) *biz.SfOrderSearchQuery {
	return &biz.SfOrderSearchQuery{
		OrderID:       in.GetOrderId(),
		SearchType:    in.GetSearchType(),
		Language:      in.GetLanguage(),
		MainWaybillNo: in.GetMainWaybillNo(),
	}
}

// convertSfOrderSearchReply 订单结果查询结果转响应
func convertSfOrderSearchReply(in *biz.SfOrderSearchResult) *v1.ExpReceSearchOrderRespReply {
	return &v1.ExpReceSearchOrderRespReply{
		OrderId:                in.OrderID,
		MainWaybillNo:          in.MainWaybillNo,
		WaybillNoInfoList:      convertSfWaybillNoInfos(in.WaybillNoInfos),
		ResStatus:              in.ResStatus,
		OriginCode:             in.OriginCode,
		DestCode:               in.DestCode,
		FilterResult:           in.FilterResult,
		Remark:                 sfValue(in.Remark),
		RouteLabelInfo:         convertSfRouteLabelInfos(in.RouteLabelInfos),
		ContactInfo:            sfValue(in.ContactInfo),
		ClientCode:             in.ClientCode,
		ServiceList:            sfValue(in.ServiceList),
		ReturnExtraInfoList:    sfValue(in.ReturnExtraInfoList),
		DeliverConsigneeRemark: in.DeliverConsigneeRemark,
		IsDelete:               in.IsDelete,
		BackWaybillNo:          in.BackWaybillNo,
		TotalQty:               in.TotalQty,
		Success:                in.Success,
		ErrorCode:              in.ErrorCode,
		ErrorMsg:               in.ErrorMsg,
	}
}

// convertSfDeliverTmQuery 时效价格查询请求转 DO
func convertSfDeliverTmQuery(in *v1.ExpReceQueryDeliverTmRequest) *biz.SfDeliverTmQuery {
	return &biz.SfDeliverTmQuery{
		BusinessType:  in.GetBusinessType(),
		ConsignedTime: in.GetConsignedTime(),
		SrcAddress:    convertSfAddress(in.GetSrcAddress()),
		DestAddress:   convertSfAddress(in.GetDestAddress()),
		SearchPrice:   in.GetSearchPrice(),
		Weight:        sfFloat64(in.Weight),
		Volume:        in.GetVolume(),
		MonthlyCard:   in.GetMonthlyCard(),
	}
}

// convertSfAddress 寄收地址 DTO 转 DO，nil 时返回 nil
func convertSfAddress(in *v1.Address) *biz.SfAddress {
	if in == nil {
		return nil
	}
	return &biz.SfAddress{
		Province: in.GetProvince(),
		City:     in.GetCity(),
		District: in.GetDistrict(),
		Address:  in.GetAddress(),
		Code:     in.GetCode(),
	}
}

// convertSfDeliverTmReply 时效价格查询结果转响应
func convertSfDeliverTmReply(in *biz.SfDeliverTmResult) *v1.ExpReceQueryDeliverTmReply {
	reply := &v1.ExpReceQueryDeliverTmReply{
		DeliverTmDto: make([]*v1.DeliverTmDto, 0, len(in.DeliverTmDtos)),
		Success:      in.Success,
		ErrorCode:    in.ErrorCode,
		ErrorMsg:     in.ErrorMsg,
	}
	for _, d := range in.DeliverTmDtos {
		reply.DeliverTmDto = append(reply.DeliverTmDto, &v1.DeliverTmDto{
			BusinessType:     d.BusinessType,
			BusinessTypeDesc: d.BusinessTypeDesc,
			DeliverTime:      d.DeliverTime,
			SearchPrice:      d.SearchPrice,
			Fee:              d.Fee,
			CloseTime:        d.CloseTime,
		})
	}
	return reply
}

// sfLogJSON 把 protobuf 消息序列化为 JSON 以便排查问题，序列化失败时退回结构体输出
func sfLogJSON(m proto.Message) string {
	if m == nil {
		return ""
	}
	data, err := protojson.Marshal(m)
	if err != nil {
		return truncateSfLog(fmt.Sprintf("%+v", m))
	}
	return truncateSfLog(string(data))
}

// truncateSfLog 按字符截断日志，避免在多字节字符中间截断出现乱码
func truncateSfLog(s string) string {
	runes := []rune(s)
	if len(runes) <= sfLogMaxLen {
		return s
	}
	return string(runes[:sfLogMaxLen]) + "...(truncated)"
}

// maskSfToken 访问令牌不落完整明文，仅保留前 4 位便于比对是否为同一个令牌
func maskSfToken(token string) string {
	if len(token) <= 4 {
		return "****"
	}
	return token[:4] + "****"
}

// sfFloat64 复制可选 double 字段的取值，避免 DO 与 DTO 共享同一块内存
func sfFloat64(in *float64) *float64 {
	if in == nil {
		return nil
	}
	value := *in
	return &value
}

// sfValue 把顺丰透传报文的 DO 值转成 proto Value，空值返回 nil
func sfValue(in any) *structpb.Value {
	if in == nil {
		return nil
	}
	value, err := structpb.NewValue(in)
	if err != nil {
		// 非 JSON 可表示的值（顺丰报文不会出现）时退化为空值，保证主流程可用
		return structpb.NewNullValue()
	}
	return value
}

// convertSfCreateOrder 下单请求转 DO
func convertSfCreateOrder(in *v1.ExpReceCreateOrderRequest) *biz.SfCreateOrder {
	order := &biz.SfCreateOrder{
		OrderID:      in.GetOrderId(),
		MonthlyCard:  in.GetMonthlyCard(),
		Language:     in.GetLanguage(),
		ContactInfos: make([]biz.SfContactInfo, 0, len(in.GetContactInfoList())),
		CargoDetails: make([]biz.SfCargoDetail, 0, len(in.GetCargoDetails())),
		ServiceList:  make([]biz.SfServiceItem, 0, len(in.GetServiceList())),
	}
	for _, c := range in.GetContactInfoList() {
		order.ContactInfos = append(order.ContactInfos, biz.SfContactInfo{
			ContactType: c.GetContactType(),
			Contact:     c.GetContact(),
			Company:     c.GetCompany(),
			Tel:         c.GetTel(),
			Country:     c.GetCountry(),
			PostCode:    c.GetPostCode(),
			Address:     c.GetAddress(),
		})
	}
	for _, c := range in.GetCargoDetails() {
		order.CargoDetails = append(order.CargoDetails, biz.SfCargoDetail{
			Name:       c.GetName(),
			Count:      c.GetCount(),
			Unit:       c.GetUnit(),
			Weight:     c.GetWeight(),
			Amount:     c.GetAmount(),
			Currency:   c.GetCurrency(),
			SourceArea: c.GetSourceArea(),
		})
	}
	for _, s := range in.GetServiceList() {
		order.ServiceList = append(order.ServiceList, biz.SfServiceItem{
			Name:   s.GetName(),
			Value:  s.GetValue(),
			Value1: s.GetValue1(),
			Value2: s.GetValue2(),
		})
	}
	return order
}

// convertSfCreateOrderReply 下单结果转响应
func convertSfCreateOrderReply(in *biz.SfCreateOrderResult) *v1.ExpReceCreateOrderReply {
	return &v1.ExpReceCreateOrderReply{
		OrderId:                   in.OrderID,
		OriginCode:                in.OriginCode,
		DestCode:                  in.DestCode,
		FilterResult:              in.FilterResult,
		Remark:                    in.Remark,
		Url:                       in.URL,
		PaymentLink:               in.PaymentLink,
		IsUpstairs:                in.IsUpstairs,
		IsSpecialWarehouseService: in.IsSpecialWarehouseService,
		MappingMark:               in.MappingMark,
		AgentMailno:               in.AgentMailno,
		ReturnExtraInfoList:       sfValue(in.ReturnExtraInfoList),
		WaybillNoInfoList:         convertSfWaybillNoInfos(in.WaybillNoInfos),
		RouteLabelInfo:            convertSfRouteLabelInfos(in.RouteLabelInfos),
		ContactInfoList:           sfValue(in.ContactInfoList),
		SendStartTm:               in.SendStartTm,
		CustomerRights:            sfValue(in.CustomerRights),
		ExpressTypeId:             sfValue(in.ExpressTypeID),
		Success:                   in.Success,
		ErrorCode:                 in.ErrorCode,
		ErrorMsg:                  in.ErrorMsg,
	}
}

// convertSfUpdateOrder 订单确认/取消请求转 DO
func convertSfUpdateOrder(in *v1.ExpReceUpdateOrderRequest) *biz.SfUpdateOrder {
	order := &biz.SfUpdateOrder{
		OrderID:        in.GetOrderId(),
		DealType:       in.GetDealType(),
		Language:       in.GetLanguage(),
		TotalWeight:    in.GetTotalWeight(),
		WaybillNoInfos: make([]biz.SfWaybillNoInfo, 0, len(in.GetWaybillNoInfoList())),
	}
	for _, w := range in.GetWaybillNoInfoList() {
		order.WaybillNoInfos = append(order.WaybillNoInfos, biz.SfWaybillNoInfo{
			WaybillType: w.GetWaybillType(),
			WaybillNo:   w.GetWaybillNo(),
		})
	}
	return order
}

// convertSfUpdateOrderReply 订单确认/取消结果转响应
func convertSfUpdateOrderReply(in *biz.SfUpdateOrderResult) *v1.ExpReceUpdateOrderReply {
	return &v1.ExpReceUpdateOrderReply{
		OrderId:           in.OrderID,
		WaybillNoInfoList: convertSfWaybillNoInfos(in.WaybillNoInfos),
		ResStatus:         in.ResStatus,
		ExtraInfoList:     sfValue(in.ExtraInfoList),
		Success:           in.Success,
		ErrorCode:         in.ErrorCode,
		ErrorMsg:          in.ErrorMsg,
	}
}

// convertSfInterceptOrder 截单转寄退回请求转 DO
func convertSfInterceptOrder(in *v1.ExpReceWantedInterceptRequest) *biz.SfInterceptOrder {
	return &biz.SfInterceptOrder{
		WaybillNo:      in.GetWaybillNo(),
		ServiceCode:    in.GetServiceCode(),
		Role:           in.GetRole(),
		PayMode:        in.GetPayMode(),
		MonthlyCardNo:  in.GetMonthlyCardNo(),
		ProductType:    in.GetProductType(),
		CodAmount:      sfFloat64(in.CodAmount),
		DeliverDate:    in.GetDeliverDate(),
		DeliverTimeMin: in.GetDeliverTimeMin(),
		DeliverTimeMax: in.GetDeliverTimeMax(),
		SelfPickPoint:  in.GetSelfPickPoint(),
		NewDestAddress: convertSfNewDestAddress(in.GetNewDestAddress()),
		Cancel:         in.GetCancel(),
		CusID:          in.GetCusId(),
	}
}

// convertSfNewDestAddress 新目的地信息 DTO 转 DO，nil 时返回 nil
func convertSfNewDestAddress(in *v1.NewDestAddress) *biz.SfNewDestAddress {
	if in == nil {
		return nil
	}
	return &biz.SfNewDestAddress{
		Country:      in.GetCountry(),
		CountryCode:  in.GetCountryCode(),
		Province:     in.GetProvince(),
		City:         in.GetCity(),
		County:       in.GetCounty(),
		Address:      in.GetAddress(),
		Company:      in.GetCompany(),
		Contact:      in.GetContact(),
		Phone:        in.GetPhone(),
		AreaCode:     in.GetAreaCode(),
		LocationCode: in.GetLocationCode(),
	}
}

// convertSfInterceptReply 截单转寄退回结果转响应
func convertSfInterceptReply(in *biz.SfInterceptResult) *v1.ExpReceWantedInterceptReply {
	reply := &v1.ExpReceWantedInterceptReply{
		CusId:     in.CusID,
		Amount:    in.Amount,
		Success:   in.Success,
		ErrorCode: in.ErrorCode,
		ErrorMsg:  in.ErrorMsg,
	}
	if in.Freight != nil {
		reply.FreightAdditionInfoResp = &v1.FreightAdditionInfoResp{
			InterceptionDeptCode: in.Freight.InterceptionDeptCode,
			TransferFlg:          in.Freight.TransferFlg,
		}
	}
	return reply
}

// convertSfFilterOrders 订单筛选请求转 DO
func convertSfFilterOrders(in *v1.ExpReceFilterOrderBspRequest) *biz.SfFilterOrders {
	orders := &biz.SfFilterOrders{
		Orders: make([]biz.SfFilterOrder, 0, len(in.GetOrderFilterList())),
	}
	for _, f := range in.GetOrderFilterList() {
		order := biz.SfFilterOrder{
			FilterType:   f.GetFilterType(),
			OrderID:      f.GetOrderId(),
			MonthlyCard:  f.GetMonthlyCard(),
			ContactInfos: make([]biz.SfFilterAddr, 0, len(f.GetContactInfos())),
		}
		for _, c := range f.GetContactInfos() {
			order.ContactInfos = append(order.ContactInfos, biz.SfFilterAddr{
				ContactType: c.GetContactType(),
				Tel:         c.GetTel(),
				Country:     c.GetCountry(),
				Province:    c.GetProvince(),
				City:        c.GetCity(),
				County:      c.GetCounty(),
				Address:     c.GetAddress(),
				PostCode:    c.GetPostCode(),
			})
		}
		orders.Orders = append(orders.Orders, order)
	}
	return orders
}

// convertSfFilterOrdersReply 订单筛选结果转响应
func convertSfFilterOrdersReply(in *biz.SfFilterOrdersResult) *v1.ExpReceFilterOrderBspReply {
	reply := &v1.ExpReceFilterOrderBspReply{
		ResDtos:   make([]*v1.FilterOrderResult, 0, len(in.Results)),
		Success:   in.Success,
		ErrorCode: in.ErrorCode,
		ErrorMsg:  in.ErrorMsg,
	}
	for _, r := range in.Results {
		reply.ResDtos = append(reply.ResDtos, &v1.FilterOrderResult{
			OrderId:      r.OrderID,
			FilterResult: r.FilterResult,
			OriginCode:   r.OriginCode,
			DestCode:     r.DestCode,
			Remark:       r.Remark,
		})
	}
	return reply
}

// convertSfWaybillNoInfos 运单号信息 DO 转响应
func convertSfWaybillNoInfos(in []biz.SfWaybillNoInfo) []*v1.WaybillNoInfo {
	if len(in) == 0 {
		return nil
	}
	infos := make([]*v1.WaybillNoInfo, 0, len(in))
	for _, w := range in {
		infos = append(infos, &v1.WaybillNoInfo{
			WaybillType: w.WaybillType,
			WaybillNo:   w.WaybillNo,
			BoxNo:       w.BoxNo,
			Length:      w.Length,
			Width:       w.Width,
			Height:      w.Height,
			Weight:      w.Weight,
			Volume:      w.Volume,
		})
	}
	return infos
}

// convertSfRouteLabelInfos 路由标签信息 DO 转响应
func convertSfRouteLabelInfos(in []biz.SfRouteLabelInfo) []*v1.RouteLabelInfo {
	if len(in) == 0 {
		return nil
	}
	infos := make([]*v1.RouteLabelInfo, 0, len(in))
	for _, r := range in {
		infos = append(infos, &v1.RouteLabelInfo{
			Code:           r.Code,
			RouteLabelData: convertSfRouteLabel(r.RouteLabel),
			Message:        r.Message,
		})
	}
	return infos
}

// convertSfRouteLabel 路由标签数据 DO 转响应，nil 时返回 nil
func convertSfRouteLabel(in *biz.SfRouteLabel) *v1.RouteLabelData {
	if in == nil {
		return nil
	}
	return &v1.RouteLabelData{
		WaybillNo:              in.WaybillNo,
		SourceTransferCode:     in.SourceTransferCode,
		SourceCityCode:         in.SourceCityCode,
		SourceDeptCode:         in.SourceDeptCode,
		SourceTeamCode:         in.SourceTeamCode,
		DestCityCode:           in.DestCityCode,
		DestDeptCode:           in.DestDeptCode,
		DestDeptCodeMapping:    in.DestDeptCodeMapping,
		DestTeamCode:           in.DestTeamCode,
		DestTeamCodeMapping:    in.DestTeamCodeMapping,
		DestTransferCode:       in.DestTransferCode,
		DestRouteLabel:         in.DestRouteLabel,
		ProName:                in.ProName,
		ProCode:                in.ProCode,
		CargoTypeCode:          in.CargoTypeCode,
		LimitTypeCode:          in.LimitTypeCode,
		ExpressTypeCode:        in.ExpressTypeCode,
		CodingMapping:          in.CodingMapping,
		CodingMappingOut:       in.CodingMappingOut,
		XbFlag:                 in.XBFlag,
		PrintFlag:              in.PrintFlag,
		PrintIcon:              in.PrintIcon,
		TwoDimensionCode:       in.TwoDimensionCode,
		AbFlag:                 in.ABFlag,
		DestPortCode:           in.DestPortCode,
		DestCountry:            in.DestCountry,
		DestPostCode:           in.DestPostCode,
		GoodsValueTotal:        in.GoodsValueTotal,
		CurrencySymbol:         in.CurrencySymbol,
		CusBatch:               in.CusBatch,
		GoodsNumber:            in.GoodsNumber,
		ErrMsg:                 in.ErrMsg,
		CheckCode:              in.CheckCode,
		ProIcon:                in.ProIcon,
		FileIcon:               in.FileIcon,
		FbaIcon:                in.FbaIcon,
		IcsmIcon:               in.IcsmIcon,
		DestGisDeptCode:        in.DestGisDeptCode,
		NewIcon:                sfValue(in.NewIcon),
		DestAddrKeyWord:        in.DestAddrKeyWord,
		SendAreaCode:           in.SendAreaCode,
		DestinationStationCode: in.DestinationStationCode,
		SxLabelDestCode:        in.SxLabelDestCode,
		SxDestTransferCode:     in.SxDestTransferCode,
		SxCompany:              in.SxCompany,
		NewAbFlag:              in.NewABFlag,
		RongType:               sfValue(in.RongType),
		WaybillIconList:        sfValue(in.WaybillIconList),
	}
}
