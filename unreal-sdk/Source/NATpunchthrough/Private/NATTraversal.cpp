#include "NATTraversal.h"
#include "NATpunchthroughModule.h"
#include "WebSocketsModule.h"
#include "IWebSocket.h"
#include "Serialization/JsonReader.h"
#include "Serialization/JsonSerializer.h"
#include "Serialization/JsonWriter.h"
#include "Dom/JsonObject.h"
#include "Dom/JsonValue.h"
#include "SocketSubsystem.h"
#include "Sockets.h"
#include "IPAddress.h"
#include "Async/Async.h"
#include "TimerManager.h"
#include "Engine/World.h"
#include "HttpModule.h"
#include "Interfaces/IHttpRequest.h"
#include "Interfaces/IHttpResponse.h"

UNATTraversal::UNATTraversal()
{
}

void UNATTraversal::BeginDestroy()
{
	DisconnectSignaling();
	StopPunch();
	Super::BeginDestroy();
}

// =============================================================================
// STUN Discovery
// =============================================================================

void UNATTraversal::DiscoverNAT(const FString& StunServer, int32 StunPort)
{
	TWeakObjectPtr<UNATTraversal> WeakThis(this);

	AsyncTask(ENamedThreads::AnyBackgroundThreadNormalTask, [WeakThis, StunServer, StunPort]()
	{
		if (!WeakThis.IsValid()) return;
		WeakThis->PerformStunBinding(StunServer, StunPort);
	});
}

void UNATTraversal::PerformStunBinding(const FString& StunServer, int32 StunPort)
{
	ISocketSubsystem* SocketSub = ISocketSubsystem::Get(PLATFORM_SOCKETSUBSYSTEM);
	FStunResult Result;

	// Resolve STUN server address
	FAddressInfoResult GAIResult = SocketSub->GetAddressInfo(*StunServer, nullptr, EAddressInfoFlags::Default, NAME_None);
	if (GAIResult.ReturnCode != SE_NO_ERROR || GAIResult.Results.Num() == 0)
	{
		Result.Error = FString::Printf(TEXT("Failed to resolve STUN server: %s"), *StunServer);
		TWeakObjectPtr<UNATTraversal> WeakThis(this);
		AsyncTask(ENamedThreads::GameThread, [WeakThis, Result]()
		{
			if (WeakThis.IsValid()) WeakThis->OnStunDiscoveryComplete.Broadcast(Result);
		});
		return;
	}

	TSharedRef<FInternetAddr> StunAddr = GAIResult.Results[0].Address->Clone();
	StunAddr->SetPort(StunPort);

	// Create UDP socket
	FSocket* Socket = SocketSub->CreateSocket(NAME_DGram, TEXT("STUNSocket"), false);
	if (!Socket)
	{
		Result.Error = TEXT("Failed to create UDP socket for STUN");
		TWeakObjectPtr<UNATTraversal> WeakThis(this);
		AsyncTask(ENamedThreads::GameThread, [WeakThis, Result]()
		{
			if (WeakThis.IsValid()) WeakThis->OnStunDiscoveryComplete.Broadcast(Result);
		});
		return;
	}

	Socket->SetNonBlocking(false);

	// Bind to any local port
	TSharedRef<FInternetAddr> LocalAddr = SocketSub->CreateInternetAddr();
	LocalAddr->SetAnyAddress();
	LocalAddr->SetPort(0);
	Socket->Bind(*LocalAddr);

	// Get the local port we bound to (for NAT type comparison)
	TSharedRef<FInternetAddr> BoundAddr = SocketSub->CreateInternetAddr();
	Socket->GetAddress(*BoundAddr);
	int32 LocalBoundPort = BoundAddr->GetPort();

	// Build and send STUN Binding Request (RFC 5389)
	TArray<uint8> TransactionId;
	TArray<uint8> StunRequest = BuildStunBindingRequest(TransactionId);

	int32 BytesSent = 0;
	if (!Socket->SendTo(StunRequest.GetData(), StunRequest.Num(), BytesSent, *StunAddr))
	{
		Result.Error = TEXT("Failed to send STUN binding request");
		Socket->Close();
		SocketSub->DestroySocket(Socket);
		TWeakObjectPtr<UNATTraversal> WeakThis(this);
		AsyncTask(ENamedThreads::GameThread, [WeakThis, Result]()
		{
			if (WeakThis.IsValid()) WeakThis->OnStunDiscoveryComplete.Broadcast(Result);
		});
		return;
	}

	// Wait for response (up to 3 seconds)
	if (!Socket->Wait(ESocketWaitConditions::WaitForRead, FTimespan::FromSeconds(3.0)))
	{
		Result.Error = TEXT("STUN request timed out");
		Socket->Close();
		SocketSub->DestroySocket(Socket);
		TWeakObjectPtr<UNATTraversal> WeakThis(this);
		AsyncTask(ENamedThreads::GameThread, [WeakThis, Result]()
		{
			if (WeakThis.IsValid()) WeakThis->OnStunDiscoveryComplete.Broadcast(Result);
		});
		return;
	}

	uint8 RecvBuffer[2048];
	int32 BytesRead = 0;
	TSharedRef<FInternetAddr> SenderAddr = SocketSub->CreateInternetAddr();
	if (!Socket->RecvFrom(RecvBuffer, sizeof(RecvBuffer), BytesRead, *SenderAddr))
	{
		Result.Error = TEXT("Failed to receive STUN response");
		Socket->Close();
		SocketSub->DestroySocket(Socket);
		TWeakObjectPtr<UNATTraversal> WeakThis(this);
		AsyncTask(ENamedThreads::GameThread, [WeakThis, Result]()
		{
			if (WeakThis.IsValid()) WeakThis->OnStunDiscoveryComplete.Broadcast(Result);
		});
		return;
	}

	// Send a second binding request from the same socket to detect NAT type
	TArray<uint8> TransactionId2;
	TArray<uint8> StunRequest2 = BuildStunBindingRequest(TransactionId2);
	Socket->SendTo(StunRequest2.GetData(), StunRequest2.Num(), BytesSent, *StunAddr);

	FString MappedIP2;
	int32 MappedPort2 = 0;
	bool bGotSecondResponse = false;

	if (Socket->Wait(ESocketWaitConditions::WaitForRead, FTimespan::FromSeconds(2.0)))
	{
		uint8 RecvBuffer2[2048];
		int32 BytesRead2 = 0;
		TSharedRef<FInternetAddr> SenderAddr2 = SocketSub->CreateInternetAddr();
		if (Socket->RecvFrom(RecvBuffer2, sizeof(RecvBuffer2), BytesRead2, *SenderAddr2))
		{
			TArray<uint8> ResponseData2(RecvBuffer2, BytesRead2);
			bGotSecondResponse = ParseStunBindingResponse(ResponseData2, TransactionId2, MappedIP2, MappedPort2);
		}
	}

	Socket->Close();
	SocketSub->DestroySocket(Socket);

	// Parse first response
	TArray<uint8> ResponseData(RecvBuffer, BytesRead);
	FString MappedIP;
	int32 MappedPort = 0;

	if (ParseStunBindingResponse(ResponseData, TransactionId, MappedIP, MappedPort))
	{
		Result.bSuccess = true;
		Result.PublicIP = MappedIP;
		Result.PublicPort = MappedPort;

		// NAT type heuristic based on port mapping behavior
		if (MappedPort == LocalBoundPort)
		{
			// Port preserved — likely Open or Full Cone
			Result.NATType = ENATType::Open;
		}
		else if (bGotSecondResponse && MappedPort == MappedPort2)
		{
			// Same external port for two requests to same server — likely Full Cone or Moderate
			Result.NATType = ENATType::Moderate;
		}
		else if (bGotSecondResponse && MappedPort != MappedPort2)
		{
			// Different external port each time — Symmetric NAT
			Result.NATType = ENATType::Symmetric;
		}
		else
		{
			// Couldn't determine, assume moderate
			Result.NATType = ENATType::Moderate;
		}

		TWeakObjectPtr<UNATTraversal> WeakThis(this);
		AsyncTask(ENamedThreads::GameThread, [WeakThis, Result]()
		{
			if (WeakThis.IsValid())
			{
				WeakThis->PublicIP = Result.PublicIP;
				WeakThis->PublicPort = Result.PublicPort;
				WeakThis->DetectedNATType = Result.NATType;
				WeakThis->OnStunDiscoveryComplete.Broadcast(Result);
			}
		});
	}
	else
	{
		Result.Error = TEXT("Failed to parse STUN response");
		TWeakObjectPtr<UNATTraversal> WeakThis(this);
		AsyncTask(ENamedThreads::GameThread, [WeakThis, Result]()
		{
			if (WeakThis.IsValid()) WeakThis->OnStunDiscoveryComplete.Broadcast(Result);
		});
	}
}

TArray<uint8> UNATTraversal::BuildStunBindingRequest(TArray<uint8>& OutTransactionId)
{
	TArray<uint8> Packet;

	// Message Type: 0x0001 (Binding Request)
	Packet.Add(0x00);
	Packet.Add(0x01);

	// Message Length: 0 (no attributes)
	Packet.Add(0x00);
	Packet.Add(0x00);

	// Magic Cookie: 0x2112A442 (RFC 5389)
	Packet.Add(0x21);
	Packet.Add(0x12);
	Packet.Add(0xA4);
	Packet.Add(0x42);

	// Transaction ID: 12 random bytes
	OutTransactionId.SetNum(12);
	for (int32 i = 0; i < 12; i++)
	{
		OutTransactionId[i] = static_cast<uint8>(FMath::RandRange(0, 255));
	}
	Packet.Append(OutTransactionId);

	return Packet;
}

bool UNATTraversal::ParseStunBindingResponse(const TArray<uint8>& Data, const TArray<uint8>& TransactionId, FString& OutIP, int32& OutPort)
{
	if (Data.Num() < 20) return false;

	// Verify message type is Binding Success Response (0x0101)
	uint16 MsgType = (static_cast<uint16>(Data[0]) << 8) | Data[1];
	if (MsgType != 0x0101) return false;

	uint16 MsgLength = (static_cast<uint16>(Data[2]) << 8) | Data[3];

	// Verify magic cookie
	if (Data[4] != 0x21 || Data[5] != 0x12 || Data[6] != 0xA4 || Data[7] != 0x42) return false;

	// Verify transaction ID
	for (int32 i = 0; i < 12; i++)
	{
		if (Data[8 + i] != TransactionId[i]) return false;
	}

	// Parse attributes looking for XOR-MAPPED-ADDRESS (0x0020) or MAPPED-ADDRESS (0x0001)
	int32 Offset = 20;
	int32 End = FMath::Min(20 + static_cast<int32>(MsgLength), Data.Num());

	while (Offset + 4 <= End)
	{
		uint16 AttrType = (static_cast<uint16>(Data[Offset]) << 8) | Data[Offset + 1];
		uint16 AttrLen = (static_cast<uint16>(Data[Offset + 2]) << 8) | Data[Offset + 3];
		Offset += 4;

		if (Offset + AttrLen > End) break;

		if (AttrType == 0x0020) // XOR-MAPPED-ADDRESS (preferred)
		{
			if (AttrLen >= 8)
			{
				uint8 Family = Data[Offset + 1];
				if (Family == 0x01) // IPv4
				{
					uint16 XorPort = (static_cast<uint16>(Data[Offset + 2]) << 8) | Data[Offset + 3];
					OutPort = XorPort ^ 0x2112;

					uint8 IP[4];
					IP[0] = Data[Offset + 4] ^ 0x21;
					IP[1] = Data[Offset + 5] ^ 0x12;
					IP[2] = Data[Offset + 6] ^ 0xA4;
					IP[3] = Data[Offset + 7] ^ 0x42;
					OutIP = FString::Printf(TEXT("%d.%d.%d.%d"), IP[0], IP[1], IP[2], IP[3]);
					return true;
				}
			}
		}
		else if (AttrType == 0x0001) // MAPPED-ADDRESS (fallback)
		{
			if (AttrLen >= 8)
			{
				uint8 Family = Data[Offset + 1];
				if (Family == 0x01) // IPv4
				{
					OutPort = (static_cast<int32>(Data[Offset + 2]) << 8) | Data[Offset + 3];
					OutIP = FString::Printf(TEXT("%d.%d.%d.%d"),
						Data[Offset + 4], Data[Offset + 5], Data[Offset + 6], Data[Offset + 7]);
					return true;
				}
			}
		}

		// Align to 4-byte boundary
		Offset += AttrLen;
		if (AttrLen % 4 != 0) Offset += (4 - (AttrLen % 4));
	}

	return false;
}

// =============================================================================
// UPnP Port Mapping
// =============================================================================

FString UNATTraversal::BuildSOAPAddPortMapping(int32 Port, const FString& LocalIP)
{
	return FString::Printf(TEXT(
		"<?xml version=\"1.0\"?>"
		"<s:Envelope xmlns:s=\"http://schemas.xmlsoap.org/soap/envelope/\" s:encodingStyle=\"http://schemas.xmlsoap.org/soap/encoding/\">"
		"<s:Body>"
		"<u:AddPortMapping xmlns:u=\"urn:schemas-upnp-org:service:WANIPConnection:1\">"
		"<NewRemoteHost></NewRemoteHost>"
		"<NewExternalPort>%d</NewExternalPort>"
		"<NewProtocol>UDP</NewProtocol>"
		"<NewInternalPort>%d</NewInternalPort>"
		"<NewInternalClient>%s</NewInternalClient>"
		"<NewEnabled>1</NewEnabled>"
		"<NewPortMappingDescription>NATPunchthrough</NewPortMappingDescription>"
		"<NewLeaseDuration>3600</NewLeaseDuration>"
		"</u:AddPortMapping>"
		"</s:Body>"
		"</s:Envelope>"),
		Port, Port, *LocalIP);
}

FString UNATTraversal::BuildSOAPDeletePortMapping(int32 Port)
{
	return FString::Printf(TEXT(
		"<?xml version=\"1.0\"?>"
		"<s:Envelope xmlns:s=\"http://schemas.xmlsoap.org/soap/envelope/\" s:encodingStyle=\"http://schemas.xmlsoap.org/soap/encoding/\">"
		"<s:Body>"
		"<u:DeletePortMapping xmlns:u=\"urn:schemas-upnp-org:service:WANIPConnection:1\">"
		"<NewRemoteHost></NewRemoteHost>"
		"<NewExternalPort>%d</NewExternalPort>"
		"<NewProtocol>UDP</NewProtocol>"
		"</u:DeletePortMapping>"
		"</s:Body>"
		"</s:Envelope>"),
		Port);
}

void UNATTraversal::TryUPnP(int32 Port, int32 TimeoutMs)
{
	TWeakObjectPtr<UNATTraversal> WeakThis(this);

	AsyncTask(ENamedThreads::AnyBackgroundThreadNormalTask, [WeakThis, Port, TimeoutMs]()
	{
		if (!WeakThis.IsValid()) return;
		WeakThis->PerformUPnPMapping(Port, TimeoutMs);
	});
}

void UNATTraversal::PerformUPnPMapping(int32 Port, int32 TimeoutMs)
{
	ISocketSubsystem* SocketSub = ISocketSubsystem::Get(PLATFORM_SOCKETSUBSYSTEM);
	FUPnPResult Result;

	// Step 1: SSDP Discovery — find Internet Gateway Device
	FSocket* Socket = SocketSub->CreateSocket(NAME_DGram, TEXT("UPnPDiscovery"), false);
	if (!Socket)
	{
		Result.Error = TEXT("Failed to create UPnP discovery socket");
		TWeakObjectPtr<UNATTraversal> WeakThis(this);
		AsyncTask(ENamedThreads::GameThread, [WeakThis, Result]() { if (WeakThis.IsValid()) WeakThis->OnUPnPComplete.Broadcast(Result); });
		return;
	}

	Socket->SetNonBlocking(false);
	Socket->SetBroadcast(true);
	Socket->SetReuseAddr(true);

	TSharedRef<FInternetAddr> BindAddr = SocketSub->CreateInternetAddr();
	BindAddr->SetAnyAddress();
	BindAddr->SetPort(0);
	Socket->Bind(*BindAddr);

	// M-SEARCH for WANIPConnection (more specific than IGD root)
	FString SearchMsg =
		TEXT("M-SEARCH * HTTP/1.1\r\n")
		TEXT("HOST: 239.255.255.250:1900\r\n")
		TEXT("MAN: \"ssdp:discover\"\r\n")
		TEXT("MX: 2\r\n")
		TEXT("ST: urn:schemas-upnp-org:service:WANIPConnection:1\r\n")
		TEXT("\r\n");

	FTCHARToUTF8 SearchMsgUtf8(*SearchMsg);
	TSharedRef<FInternetAddr> MulticastAddr = SocketSub->CreateInternetAddr();
	bool bIsValid;
	MulticastAddr->SetIp(TEXT("239.255.255.250"), bIsValid);
	MulticastAddr->SetPort(1900);

	int32 BytesSent;
	Socket->SendTo(reinterpret_cast<const uint8*>(SearchMsgUtf8.Get()), SearchMsgUtf8.Length(), BytesSent, *MulticastAddr);

	if (!Socket->Wait(ESocketWaitConditions::WaitForRead, FTimespan::FromMilliseconds(TimeoutMs)))
	{
		Result.Error = TEXT("UPnP: no gateway device responded (SSDP timed out)");
		Socket->Close();
		SocketSub->DestroySocket(Socket);
		TWeakObjectPtr<UNATTraversal> WeakThis(this);
		AsyncTask(ENamedThreads::GameThread, [WeakThis, Result]() { if (WeakThis.IsValid()) WeakThis->OnUPnPComplete.Broadcast(Result); });
		return;
	}

	uint8 RecvBuffer[4096];
	int32 BytesRead = 0;
	TSharedRef<FInternetAddr> SenderAddr = SocketSub->CreateInternetAddr();
	if (!Socket->RecvFrom(RecvBuffer, sizeof(RecvBuffer) - 1, BytesRead, *SenderAddr) || BytesRead <= 0)
	{
		Result.Error = TEXT("UPnP: failed to receive SSDP response");
		Socket->Close();
		SocketSub->DestroySocket(Socket);
		TWeakObjectPtr<UNATTraversal> WeakThis(this);
		AsyncTask(ENamedThreads::GameThread, [WeakThis, Result]() { if (WeakThis.IsValid()) WeakThis->OnUPnPComplete.Broadcast(Result); });
		return;
	}
	RecvBuffer[BytesRead] = 0;
	Socket->Close();
	SocketSub->DestroySocket(Socket);

	FString SSDPResponse = UTF8_TO_TCHAR(reinterpret_cast<const char*>(RecvBuffer));

	// Parse LOCATION header
	FString LocationUrl;
	TArray<FString> Lines;
	SSDPResponse.ParseIntoArrayLines(Lines);
	for (const FString& Line : Lines)
	{
		if (Line.StartsWith(TEXT("LOCATION:"), ESearchCase::IgnoreCase) ||
			Line.StartsWith(TEXT("Location:"), ESearchCase::CaseSensitive))
		{
			LocationUrl = Line.Mid(9).TrimStartAndEnd();
			break;
		}
	}

	if (LocationUrl.IsEmpty())
	{
		Result.Error = TEXT("UPnP: no LOCATION in SSDP response");
		TWeakObjectPtr<UNATTraversal> WeakThis(this);
		AsyncTask(ENamedThreads::GameThread, [WeakThis, Result]() { if (WeakThis.IsValid()) WeakThis->OnUPnPComplete.Broadcast(Result); });
		return;
	}

	// Step 2: Fetch device description XML to find the control URL
	// We use a synchronous HTTP fetch here since we're already on a background thread
	// Parse the base URL from LocationUrl for constructing the control URL
	FString BaseUrl;
	{
		// Extract scheme + host + port from LocationUrl
		FString Scheme;
		if (FParse::SchemeNameFromURI(*LocationUrl, Scheme))
		{
			// Simple extraction: everything before the 3rd slash
			int32 SlashCount = 0;
			for (int32 i = 0; i < LocationUrl.Len(); i++)
			{
				if (LocationUrl[i] == '/')
				{
					SlashCount++;
					if (SlashCount == 3)
					{
						BaseUrl = LocationUrl.Left(i);
						break;
					}
				}
			}
			if (BaseUrl.IsEmpty()) BaseUrl = LocationUrl;
		}
		else
		{
			BaseUrl = LocationUrl;
		}
	}

	// We'll try the common control URL pattern directly since parsing XML
	// on a background thread without UE's XML parser is complex.
	// Most IGDs use /ctl/IPConn or similar. We try the SOAP request directly
	// against the base URL with common control paths.
	TArray<FString> ControlPaths = {
		TEXT("/ctl/IPConn"),
		TEXT("/upnp/control/WANIPConn1"),
		TEXT("/ctl/WANIPConnection"),
		TEXT("/WANIPConnection"),
		TEXT("/upnp/control/WANIPConnection0"),
	};

	// Get local IP for the mapping
	bool bCanBindAll;
	TSharedPtr<FInternetAddr> LocalHostAddr = SocketSub->GetLocalHostAddr(*GLog, bCanBindAll);
	FString LocalIP = LocalHostAddr.IsValid() ? LocalHostAddr->ToString(false) : TEXT("0.0.0.0");

	FString SOAPBody = BuildSOAPAddPortMapping(Port, LocalIP);

	// Try each control path — IGDs vary in their URL structure
	// Since we can't easily do synchronous HTTP from here, we dispatch to game thread
	// and use UE's HTTP module
	TWeakObjectPtr<UNATTraversal> WeakThis(this);
	AsyncTask(ENamedThreads::GameThread, [WeakThis, BaseUrl, ControlPaths, SOAPBody, Port, Result]() mutable
	{
		if (!WeakThis.IsValid()) return;

		// Try the first common control path
		FString ControlUrl = BaseUrl + ControlPaths[0];

		TSharedRef<IHttpRequest> HttpReq = FHttpModule::Get().CreateRequest();
		HttpReq->SetURL(ControlUrl);
		HttpReq->SetVerb(TEXT("POST"));
		HttpReq->SetHeader(TEXT("Content-Type"), TEXT("text/xml; charset=\"utf-8\""));
		HttpReq->SetHeader(TEXT("SOAPAction"), TEXT("\"urn:schemas-upnp-org:service:WANIPConnection:1#AddPortMapping\""));
		HttpReq->SetContentAsString(SOAPBody);

		HttpReq->OnProcessRequestComplete().BindLambda(
			[WeakThis, Port, BaseUrl, ControlPaths, SOAPBody, ControlUrl](FHttpRequestPtr Req, FHttpResponsePtr Resp, bool bOK)
			{
				if (!WeakThis.IsValid()) return;

				FUPnPResult UPnPResult;

				if (bOK && Resp.IsValid() && Resp->GetResponseCode() == 200)
				{
					UPnPResult.bSuccess = true;
					UPnPResult.ExternalPort = Port;
					WeakThis->MappedUPnPPort = Port;
					WeakThis->UPnPControlUrl = ControlUrl;
					WeakThis->ActiveConnectionMethod = EConnectionMethod::Direct;
					UE_LOG(LogNATPunchthrough, Log, TEXT("UPnP: Port %d mapped successfully"), Port);
				}
				else
				{
					int32 Code = (Resp.IsValid()) ? Resp->GetResponseCode() : 0;
					UPnPResult.Error = FString::Printf(TEXT("UPnP: SOAP AddPortMapping failed (HTTP %d). Router may not support UPnP or it is disabled."), Code);
				}

				WeakThis->OnUPnPComplete.Broadcast(UPnPResult);
			});

		HttpReq->ProcessRequest();
	});
}

void UNATTraversal::ReleaseUPnP(int32 Port)
{
	if (MappedUPnPPort == 0) return;

	int32 PortToRelease = MappedUPnPPort;
	MappedUPnPPort = 0;

	UE_LOG(LogNATPunchthrough, Log, TEXT("UPnP: Releasing port mapping for port %d"), PortToRelease);

	// Best-effort SOAP DeletePortMapping — if it fails, the lease expires on its own (3600s)
	if (!UPnPControlUrl.IsEmpty())
	{
		FString SOAPBody = BuildSOAPDeletePortMapping(PortToRelease);
		TSharedRef<IHttpRequest> HttpReq = FHttpModule::Get().CreateRequest();
		HttpReq->SetURL(UPnPControlUrl);
		HttpReq->SetVerb(TEXT("POST"));
		HttpReq->SetHeader(TEXT("Content-Type"), TEXT("text/xml; charset=\"utf-8\""));
		HttpReq->SetHeader(TEXT("SOAPAction"), TEXT("\"urn:schemas-upnp-org:service:WANIPConnection:1#DeletePortMapping\""));
		HttpReq->SetContentAsString(SOAPBody);
		HttpReq->ProcessRequest(); // Fire-and-forget
		UPnPControlUrl.Empty();
	}
}

// =============================================================================
// WebSocket Signaling
// =============================================================================

void UNATTraversal::ConnectSignaling(const FString& Url, const FString& InApiKey)
{
	if (WebSocket.IsValid() && WebSocket->IsConnected())
	{
		DisconnectSignaling();
	}

	FString WsUrl = Url;

	// Step 1: Always convert HTTP scheme to WebSocket scheme
	WsUrl.ReplaceInline(TEXT("https://"), TEXT("wss://"));
	WsUrl.ReplaceInline(TEXT("http://"), TEXT("ws://"));
	if (!WsUrl.Contains(TEXT("ws://")) && !WsUrl.Contains(TEXT("wss://")))
	{
		WsUrl = TEXT("ws://") + WsUrl;
	}

	// Step 2: Strip trailing slashes
	while (WsUrl.EndsWith(TEXT("/")))
	{
		WsUrl.LeftChopInline(1);
	}

	// Step 3: Append /ws path if not already present
	if (!WsUrl.EndsWith(TEXT("/ws")) && !WsUrl.EndsWith(TEXT("/ws/signaling")))
	{
		WsUrl += TEXT("/ws");
	}

	TMap<FString, FString> Headers;
	if (!InApiKey.IsEmpty())
	{
		Headers.Add(TEXT("X-API-Key"), InApiKey);
	}

	WebSocket = FWebSocketsModule::Get().CreateWebSocket(WsUrl, TEXT(""), Headers);

	WebSocket->OnConnected().AddUObject(this, &UNATTraversal::OnWebSocketConnected);
	WebSocket->OnConnectionError().AddUObject(this, &UNATTraversal::OnWebSocketConnectionError);
	WebSocket->OnClosed().AddUObject(this, &UNATTraversal::OnWebSocketClosed);
	WebSocket->OnMessage().AddUObject(this, &UNATTraversal::OnWebSocketMessage);

	WebSocket->Connect();
}

void UNATTraversal::DisconnectSignaling()
{
	if (WebSocket.IsValid())
	{
		if (WebSocket->IsConnected())
		{
			WebSocket->Close();
		}
		WebSocket.Reset();
	}
	bIsSignalingConnected = false;
}

void UNATTraversal::OnWebSocketConnected()
{
	bIsSignalingConnected = true;
	OnSignalingConnected.Broadcast();
}

void UNATTraversal::OnWebSocketConnectionError(const FString& Error)
{
	bIsSignalingConnected = false;
	OnSignalingError.Broadcast(FString::Printf(TEXT("WebSocket connection error: %s"), *Error));
}

void UNATTraversal::OnWebSocketClosed(int32 StatusCode, const FString& Reason, bool bWasClean)
{
	bIsSignalingConnected = false;
	OnSignalingDisconnected.Broadcast();
}

void UNATTraversal::OnWebSocketMessage(const FString& Message)
{
	TSharedPtr<FJsonObject> JsonObj;
	TSharedRef<TJsonReader<>> Reader = TJsonReaderFactory<>::Create(Message);
	if (!FJsonSerializer::Deserialize(Reader, JsonObj) || !JsonObj.IsValid())
	{
		UE_LOG(LogNATPunchthrough, Warning, TEXT("NATTraversal: Failed to parse signaling message: %s"), *Message);
		return;
	}

	FString Type = JsonObj->GetStringField(TEXT("type"));

	if (Type == TEXT("host_registered"))
	{
		FString GameId = JsonObj->GetStringField(TEXT("game_id"));
		OnHostRegistered.Broadcast(GameId);
	}
	else if (Type == TEXT("gather_candidates"))
	{
		FString SessionId = JsonObj->GetStringField(TEXT("session_id"));
		TArray<FString> StunServers;

		const TArray<TSharedPtr<FJsonValue>>* ServersArray;
		if (JsonObj->TryGetArrayField(TEXT("stun_servers"), ServersArray))
		{
			for (const auto& Val : *ServersArray)
			{
				StunServers.Add(Val->AsString());
			}
		}

		OnGatherCandidates.Broadcast(SessionId, StunServers);
	}
	else if (Type == TEXT("peer_candidate"))
	{
		FString SessionId = JsonObj->GetStringField(TEXT("session_id"));
		FICECandidate Candidate;
		Candidate.PublicIP = JsonObj->GetStringField(TEXT("public_ip"));
		Candidate.PublicPort = JsonObj->GetIntegerField(TEXT("public_port"));
		Candidate.LocalIP = JsonObj->GetStringField(TEXT("local_ip"));
		Candidate.LocalPort = JsonObj->GetIntegerField(TEXT("local_port"));
		Candidate.NATTypeString = JsonObj->GetStringField(TEXT("nat_type"));
		OnPeerCandidate.Broadcast(SessionId, Candidate);
	}
	else if (Type == TEXT("punch_signal"))
	{
		FString SessionId = JsonObj->GetStringField(TEXT("session_id"));
		FString PeerIP = JsonObj->GetStringField(TEXT("peer_ip"));
		int32 PeerPort = JsonObj->GetIntegerField(TEXT("peer_port"));
		OnPunchSignal.Broadcast(SessionId, PeerIP, PeerPort);
	}
	else if (Type == TEXT("turn_fallback"))
	{
		FTurnCredentials Creds;
		Creds.Username = JsonObj->GetStringField(TEXT("username"));
		Creds.Password = JsonObj->GetStringField(TEXT("password"));
		Creds.TTL = JsonObj->GetIntegerField(TEXT("ttl"));

		const TArray<TSharedPtr<FJsonValue>>* TurnArray;
		if (JsonObj->TryGetArrayField(TEXT("turn_server"), TurnArray))
		{
			for (const auto& Val : *TurnArray)
			{
				Creds.URIs.Add(Val->AsString());
			}
		}

		OnTurnFallback.Broadcast(Creds);
	}
	else if (Type == TEXT("peer_connected") || Type == TEXT("connection_established"))
	{
		FString PeerId = JsonObj->GetStringField(TEXT("peer_id"));
		FString Method = JsonObj->GetStringField(TEXT("method"));
		OnPeerConnected.Broadcast(PeerId, Method);
	}
	else if (Type == TEXT("error"))
	{
		FString Error = JsonObj->GetStringField(TEXT("error"));
		OnSignalingError.Broadcast(Error);
	}
}

void UNATTraversal::SendSignalingMessage(const TSharedPtr<FJsonObject>& Msg)
{
	if (!WebSocket.IsValid() || !WebSocket->IsConnected())
	{
		UE_LOG(LogNATPunchthrough, Warning, TEXT("NATTraversal: Cannot send - WebSocket not connected"));
		return;
	}

	FString MsgString;
	TSharedRef<TJsonWriter<TCHAR, TCondensedJsonPrintPolicy<TCHAR>>> Writer =
		TJsonWriterFactory<TCHAR, TCondensedJsonPrintPolicy<TCHAR>>::Create(&MsgString);
	FJsonSerializer::Serialize(Msg.ToSharedRef(), Writer);
	WebSocket->Send(MsgString);
}

void UNATTraversal::RegisterHost(const FString& GameId, const FString& HostToken)
{
	TSharedPtr<FJsonObject> Msg = MakeShareable(new FJsonObject());
	Msg->SetStringField(TEXT("type"), TEXT("register_host"));
	Msg->SetStringField(TEXT("game_id"), GameId);
	Msg->SetStringField(TEXT("host_token"), HostToken);
	SendSignalingMessage(Msg);
}

void UNATTraversal::RequestJoin(const FString& GameId, const FString& JoinCode, const FString& Password)
{
	TSharedPtr<FJsonObject> Msg = MakeShareable(new FJsonObject());
	Msg->SetStringField(TEXT("type"), TEXT("request_join"));
	Msg->SetStringField(TEXT("game_id"), GameId);
	if (!JoinCode.IsEmpty())
	{
		Msg->SetStringField(TEXT("join_code"), JoinCode);
	}
	if (!Password.IsEmpty())
	{
		Msg->SetStringField(TEXT("password"), Password);
	}
	SendSignalingMessage(Msg);
}

void UNATTraversal::SendICECandidate(const FString& SessionId, const FICECandidate& Candidate)
{
	TSharedPtr<FJsonObject> Msg = MakeShareable(new FJsonObject());
	Msg->SetStringField(TEXT("type"), TEXT("ice_candidate"));
	Msg->SetStringField(TEXT("session_id"), SessionId);
	Msg->SetStringField(TEXT("public_ip"), Candidate.PublicIP);
	Msg->SetNumberField(TEXT("public_port"), Candidate.PublicPort);
	Msg->SetStringField(TEXT("local_ip"), Candidate.LocalIP);
	Msg->SetNumberField(TEXT("local_port"), Candidate.LocalPort);
	Msg->SetStringField(TEXT("nat_type"), Candidate.NATTypeString);
	SendSignalingMessage(Msg);
}

void UNATTraversal::SendConnectionEstablished(const FString& SessionId, EConnectionMethod Method)
{
	TSharedPtr<FJsonObject> Msg = MakeShareable(new FJsonObject());
	Msg->SetStringField(TEXT("type"), TEXT("connection_established"));
	Msg->SetStringField(TEXT("session_id"), SessionId);
	Msg->SetStringField(TEXT("method"), ConnectionMethodToString(Method));
	SendSignalingMessage(Msg);
}

void UNATTraversal::SendHeartbeat()
{
	TSharedPtr<FJsonObject> Msg = MakeShareable(new FJsonObject());
	Msg->SetStringField(TEXT("type"), TEXT("heartbeat"));
	SendSignalingMessage(Msg);
}

// =============================================================================
// UDP Hole Punch
// =============================================================================

void UNATTraversal::AttemptPunch(const FString& PeerIP, int32 PeerPort, int32 LocalPort, float TimeoutSeconds)
{
	StopPunch();

	UWorld* World = OwningWorld.Get();
	if (!World)
	{
		FPunchResult Result;
		Result.Error = TEXT("No world available for punch timer. Call SetWorld() first.");
		OnPunchComplete.Broadcast(Result);
		return;
	}

	ISocketSubsystem* SocketSub = ISocketSubsystem::Get(PLATFORM_SOCKETSUBSYSTEM);

	PunchSocket = SocketSub->CreateSocket(NAME_DGram, TEXT("PunchSocket"), false);
	if (!PunchSocket)
	{
		FPunchResult Result;
		Result.Error = TEXT("Failed to create punch socket");
		OnPunchComplete.Broadcast(Result);
		return;
	}

	PunchSocket->SetNonBlocking(true);
	PunchSocket->SetReuseAddr(true);

	TSharedRef<FInternetAddr> BindAddr = SocketSub->CreateInternetAddr();
	BindAddr->SetAnyAddress();
	BindAddr->SetPort(LocalPort);
	if (!PunchSocket->Bind(*BindAddr))
	{
		// Port might be in use, try any port
		BindAddr->SetPort(0);
		PunchSocket->Bind(*BindAddr);
	}

	PunchTargetIP = PeerIP;
	PunchTargetPort = PeerPort;
	bPunching = true;

	// Send punch probes every 100ms
	World->GetTimerManager().SetTimer(PunchTimerHandle, this, &UNATTraversal::PunchTick, 0.1f, true);

	// Timeout — use weak pointer to prevent crash if object is destroyed before timer fires
	TWeakObjectPtr<UNATTraversal> WeakThis(this);
	World->GetTimerManager().SetTimer(PunchTimeoutHandle, [WeakThis]()
	{
		if (WeakThis.IsValid() && WeakThis->bPunching)
		{
			FPunchResult Result;
			Result.Error = TEXT("Hole punch timed out");
			WeakThis->StopPunch();
			WeakThis->OnPunchComplete.Broadcast(Result);
		}
	}, TimeoutSeconds, false);
}

void UNATTraversal::PunchTick()
{
	if (!bPunching || !PunchSocket) return;

	ISocketSubsystem* SocketSub = ISocketSubsystem::Get(PLATFORM_SOCKETSUBSYSTEM);

	// Send a punch probe packet
	TSharedRef<FInternetAddr> PeerAddr = SocketSub->CreateInternetAddr();
	bool bIsValid;
	PeerAddr->SetIp(*PunchTargetIP, bIsValid);
	PeerAddr->SetPort(PunchTargetPort);

	if (bIsValid)
	{
		// Both peers send identical probe: "NATPUNCH" magic bytes
		const uint8 ProbeData[] = { 'N', 'A', 'T', 'P', 'U', 'N', 'C', 'H' };
		int32 BytesSent;
		PunchSocket->SendTo(ProbeData, sizeof(ProbeData), BytesSent, *PeerAddr);
	}

	// Check for incoming probe from peer
	uint8 RecvBuffer[256];
	int32 BytesRead = 0;
	TSharedRef<FInternetAddr> SenderAddr = SocketSub->CreateInternetAddr();

	while (PunchSocket->RecvFrom(RecvBuffer, sizeof(RecvBuffer), BytesRead, *SenderAddr))
	{
		if (BytesRead >= 8 && FMemory::Memcmp(RecvBuffer, "NATPUNCH", 8) == 0)
		{
			FString SenderIP = SenderAddr->ToString(false);
			int32 SenderPort = SenderAddr->GetPort();

			FPunchResult Result;
			Result.bSuccess = true;
			Result.RemoteEndpoint = FString::Printf(TEXT("%s:%d"), *SenderIP, SenderPort);
			ActiveConnectionMethod = EConnectionMethod::StunPunch;

			StopPunch();
			OnPunchComplete.Broadcast(Result);
			return;
		}
	}
}

void UNATTraversal::StopPunch()
{
	bPunching = false;

	UWorld* World = OwningWorld.Get();
	if (World)
	{
		if (PunchTimerHandle.IsValid())
		{
			World->GetTimerManager().ClearTimer(PunchTimerHandle);
		}
		if (PunchTimeoutHandle.IsValid())
		{
			World->GetTimerManager().ClearTimer(PunchTimeoutHandle);
		}
	}

	if (PunchSocket)
	{
		PunchSocket->Close();
		ISocketSubsystem::Get(PLATFORM_SOCKETSUBSYSTEM)->DestroySocket(PunchSocket);
		PunchSocket = nullptr;
	}
}
