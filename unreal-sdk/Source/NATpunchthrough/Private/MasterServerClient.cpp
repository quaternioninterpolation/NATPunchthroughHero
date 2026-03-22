#include "MasterServerClient.h"
#include "NATpunchthroughModule.h"
#include "HttpModule.h"
#include "GenericPlatform/GenericPlatformHttp.h"
#include "Interfaces/IHttpRequest.h"
#include "Interfaces/IHttpResponse.h"
#include "Serialization/JsonReader.h"
#include "Serialization/JsonSerializer.h"
#include "Serialization/JsonWriter.h"
#include "Dom/JsonObject.h"

UMasterServerClient::UMasterServerClient()
	: ServerUrl(TEXT("http://localhost:8080"))
{
}

void UMasterServerClient::Initialize(const FString& InServerUrl, const FString& InApiKey)
{
	ServerUrl = InServerUrl;
	// Strip trailing slash
	if (ServerUrl.EndsWith(TEXT("/")))
	{
		ServerUrl.LeftChopInline(1);
	}
	ApiKey = InApiKey;
}

TSharedRef<IHttpRequest> UMasterServerClient::CreateRequest(const FString& Endpoint, const FString& Verb) const
{
	TSharedRef<IHttpRequest> Request = FHttpModule::Get().CreateRequest();
	Request->SetURL(ServerUrl + Endpoint);
	Request->SetVerb(Verb);
	Request->SetHeader(TEXT("Content-Type"), TEXT("application/json"));
	Request->SetHeader(TEXT("Accept"), TEXT("application/json"));
	return Request;
}

void UMasterServerClient::AddAuthHeaders(TSharedRef<IHttpRequest>& Request, const FString& HostToken) const
{
	if (!ApiKey.IsEmpty())
	{
		Request->SetHeader(TEXT("X-API-Key"), ApiKey);
	}
	if (!HostToken.IsEmpty())
	{
		Request->SetHeader(TEXT("Authorization"), FString::Printf(TEXT("Bearer %s"), *HostToken));
	}
}

// --- Game Registration ---

void UMasterServerClient::RegisterGame(const FGameRegistration& Registration)
{
	TSharedRef<IHttpRequest> Request = CreateRequest(TEXT("/api/games"), TEXT("POST"));
	AddAuthHeaders(Request);

	TSharedPtr<FJsonObject> Body = MakeShareable(new FJsonObject());
	Body->SetStringField(TEXT("name"), Registration.Name);
	Body->SetNumberField(TEXT("max_players"), Registration.MaxPlayers);
	Body->SetNumberField(TEXT("current_players"), Registration.CurrentPlayers);
	Body->SetStringField(TEXT("nat_type"), Registration.NATType);
	Body->SetNumberField(TEXT("host_port"), Registration.HostPort);
	Body->SetBoolField(TEXT("private"), Registration.bPrivate);

	if (!Registration.Password.IsEmpty())
	{
		Body->SetStringField(TEXT("password"), Registration.Password);
	}
	if (!Registration.Map.IsEmpty())
	{
		Body->SetStringField(TEXT("map"), Registration.Map);
	}
	if (!Registration.GameVersion.IsEmpty())
	{
		Body->SetStringField(TEXT("game_version"), Registration.GameVersion);
	}
	if (!Registration.LocalIP.IsEmpty())
	{
		Body->SetStringField(TEXT("local_ip"), Registration.LocalIP);
		Body->SetNumberField(TEXT("local_port"), Registration.LocalPort);
	}

	if (Registration.Data.Num() > 0)
	{
		TSharedPtr<FJsonObject> DataObj = MakeShareable(new FJsonObject());
		for (const auto& Pair : Registration.Data)
		{
			DataObj->SetStringField(Pair.Key, Pair.Value);
		}
		Body->SetObjectField(TEXT("data"), DataObj);
	}

	FString BodyString;
	TSharedRef<TJsonWriter<TCHAR, TCondensedJsonPrintPolicy<TCHAR>>> Writer =
		TJsonWriterFactory<TCHAR, TCondensedJsonPrintPolicy<TCHAR>>::Create(&BodyString);
	FJsonSerializer::Serialize(Body.ToSharedRef(), Writer);
	Request->SetContentAsString(BodyString);

	Request->OnProcessRequestComplete().BindUObject(this, &UMasterServerClient::HandleRegisterResponse);
	Request->ProcessRequest();
}

void UMasterServerClient::HandleRegisterResponse(FHttpRequestPtr Request, FHttpResponsePtr Response, bool bSuccess)
{
	FRegisterResult Result;

	if (!bSuccess || !Response.IsValid())
	{
		Result.Error = TEXT("Network error: failed to contact master server");
		OnError.Broadcast(Result.Error);
		OnGameRegistered.Broadcast(Result);
		return;
	}

	if (Response->GetResponseCode() != 201)
	{
		Result.Error = FString::Printf(TEXT("Server error %d: %s"), Response->GetResponseCode(), *Response->GetContentAsString());
		OnError.Broadcast(Result.Error);
		OnGameRegistered.Broadcast(Result);
		return;
	}

	TSharedPtr<FJsonObject> JsonObj;
	TSharedRef<TJsonReader<>> Reader = TJsonReaderFactory<>::Create(Response->GetContentAsString());
	if (FJsonSerializer::Deserialize(Reader, JsonObj) && JsonObj.IsValid())
	{
		Result.bSuccess = true;
		Result.GameId = JsonObj->GetStringField(TEXT("id"));
		Result.JoinCode = JsonObj->GetStringField(TEXT("join_code"));
		Result.HostToken = JsonObj->GetStringField(TEXT("host_token"));
	}
	else
	{
		Result.Error = TEXT("Failed to parse server response");
		OnError.Broadcast(Result.Error);
	}

	OnGameRegistered.Broadcast(Result);
}

// --- List Games ---

void UMasterServerClient::ListGames(const FString& Code, const FString& Version, int32 Limit, int32 Offset)
{
	FString Endpoint = FString::Printf(TEXT("/api/games?limit=%d&offset=%d"), Limit, Offset);
	if (!Code.IsEmpty())
	{
		Endpoint += FString::Printf(TEXT("&code=%s"), *FGenericPlatformHttp::UrlEncode(Code));
	}
	if (!Version.IsEmpty())
	{
		Endpoint += FString::Printf(TEXT("&version=%s"), *FGenericPlatformHttp::UrlEncode(Version));
	}

	TSharedRef<IHttpRequest> Request = CreateRequest(Endpoint, TEXT("GET"));
	AddAuthHeaders(Request);
	Request->OnProcessRequestComplete().BindUObject(this, &UMasterServerClient::HandleListResponse);
	Request->ProcessRequest();
}

void UMasterServerClient::HandleListResponse(FHttpRequestPtr Request, FHttpResponsePtr Response, bool bSuccess)
{
	TArray<FGameInfo> Games;

	if (!bSuccess || !Response.IsValid())
	{
		OnError.Broadcast(TEXT("Network error: failed to list games"));
		OnGameListReceived.Broadcast(Games);
		return;
	}

	if (Response->GetResponseCode() != 200)
	{
		OnError.Broadcast(FString::Printf(TEXT("Server error %d"), Response->GetResponseCode()));
		OnGameListReceived.Broadcast(Games);
		return;
	}

	TArray<TSharedPtr<FJsonValue>> JsonArray;
	TSharedRef<TJsonReader<>> Reader = TJsonReaderFactory<>::Create(Response->GetContentAsString());
	if (FJsonSerializer::Deserialize(Reader, JsonArray))
	{
		for (const auto& Value : JsonArray)
		{
			if (Value->Type == EJson::Object)
			{
				Games.Add(ParseGameInfo(Value->AsObject()));
			}
		}
	}

	OnGameListReceived.Broadcast(Games);
}

// --- Get Game ---

void UMasterServerClient::GetGame(const FString& GameId)
{
	TSharedRef<IHttpRequest> Request = CreateRequest(FString::Printf(TEXT("/api/games/%s"), *GameId), TEXT("GET"));
	AddAuthHeaders(Request);
	Request->OnProcessRequestComplete().BindUObject(this, &UMasterServerClient::HandleGetGameResponse);
	Request->ProcessRequest();
}

void UMasterServerClient::HandleGetGameResponse(FHttpRequestPtr Request, FHttpResponsePtr Response, bool bSuccess)
{
	if (!bSuccess || !Response.IsValid())
	{
		OnError.Broadcast(TEXT("Network error: failed to get game info"));
		return;
	}

	if (Response->GetResponseCode() != 200)
	{
		OnError.Broadcast(FString::Printf(TEXT("Game not found or server error %d"), Response->GetResponseCode()));
		return;
	}

	TSharedPtr<FJsonObject> JsonObj;
	TSharedRef<TJsonReader<>> Reader = TJsonReaderFactory<>::Create(Response->GetContentAsString());
	if (FJsonSerializer::Deserialize(Reader, JsonObj) && JsonObj.IsValid())
	{
		OnGameInfoReceived.Broadcast(ParseGameInfo(JsonObj));
	}
}

// --- Heartbeat ---

void UMasterServerClient::SendHeartbeat(const FString& GameId, const FString& HostToken)
{
	TSharedRef<IHttpRequest> Request = CreateRequest(
		FString::Printf(TEXT("/api/games/%s/heartbeat"), *GameId), TEXT("POST"));
	AddAuthHeaders(Request, HostToken);
	Request->OnProcessRequestComplete().BindUObject(this, &UMasterServerClient::HandleHeartbeatResponse);
	Request->ProcessRequest();
}

void UMasterServerClient::HandleHeartbeatResponse(FHttpRequestPtr Request, FHttpResponsePtr Response, bool bSuccess)
{
	if (!bSuccess || !Response.IsValid() || Response->GetResponseCode() != 200)
	{
		OnError.Broadcast(TEXT("Heartbeat failed"));
		return;
	}
	OnHeartbeatSent.Broadcast();
}

// --- Deregister ---

void UMasterServerClient::DeregisterGame(const FString& GameId, const FString& HostToken)
{
	TSharedRef<IHttpRequest> Request = CreateRequest(
		FString::Printf(TEXT("/api/games/%s"), *GameId), TEXT("DELETE"));
	AddAuthHeaders(Request, HostToken);
	Request->OnProcessRequestComplete().BindUObject(this, &UMasterServerClient::HandleDeregisterResponse);
	Request->ProcessRequest();
}

void UMasterServerClient::HandleDeregisterResponse(FHttpRequestPtr Request, FHttpResponsePtr Response, bool bSuccess)
{
	if (!bSuccess || !Response.IsValid() || Response->GetResponseCode() != 200)
	{
		OnError.Broadcast(TEXT("Failed to deregister game"));
		return;
	}
	OnGameDeregistered.Broadcast();
}

// --- Update Player Count ---

void UMasterServerClient::UpdatePlayerCount(const FString& GameId, const FString& HostToken, int32 CurrentPlayers)
{
	TSharedRef<IHttpRequest> Request = CreateRequest(
		FString::Printf(TEXT("/api/games/%s"), *GameId), TEXT("PATCH"));
	AddAuthHeaders(Request, HostToken);

	TSharedPtr<FJsonObject> Body = MakeShareable(new FJsonObject());
	Body->SetNumberField(TEXT("current_players"), CurrentPlayers);

	FString BodyString;
	TSharedRef<TJsonWriter<TCHAR, TCondensedJsonPrintPolicy<TCHAR>>> Writer =
		TJsonWriterFactory<TCHAR, TCondensedJsonPrintPolicy<TCHAR>>::Create(&BodyString);
	FJsonSerializer::Serialize(Body.ToSharedRef(), Writer);
	Request->SetContentAsString(BodyString);

	Request->OnProcessRequestComplete().BindUObject(this, &UMasterServerClient::HandleUpdatePlayerCountResponse);
	Request->ProcessRequest();
}

void UMasterServerClient::HandleUpdatePlayerCountResponse(FHttpRequestPtr Request, FHttpResponsePtr Response, bool bSuccess)
{
	if (!bSuccess || !Response.IsValid() || Response->GetResponseCode() != 200)
	{
		OnError.Broadcast(TEXT("Failed to update player count"));
	}
}

// --- TURN Credentials ---

void UMasterServerClient::GetTurnCredentials(const FString& GameId)
{
	TSharedRef<IHttpRequest> Request = CreateRequest(
		FString::Printf(TEXT("/api/games/%s/turn"), *GameId), TEXT("GET"));
	AddAuthHeaders(Request);
	Request->OnProcessRequestComplete().BindUObject(this, &UMasterServerClient::HandleTurnResponse);
	Request->ProcessRequest();
}

void UMasterServerClient::HandleTurnResponse(FHttpRequestPtr Request, FHttpResponsePtr Response, bool bSuccess)
{
	if (!bSuccess || !Response.IsValid())
	{
		OnError.Broadcast(TEXT("Network error: failed to get TURN credentials"));
		return;
	}

	if (Response->GetResponseCode() != 200)
	{
		OnError.Broadcast(FString::Printf(TEXT("TURN credentials error %d"), Response->GetResponseCode()));
		return;
	}

	TSharedPtr<FJsonObject> JsonObj;
	TSharedRef<TJsonReader<>> Reader = TJsonReaderFactory<>::Create(Response->GetContentAsString());
	if (FJsonSerializer::Deserialize(Reader, JsonObj) && JsonObj.IsValid())
	{
		FTurnCredentials Creds;
		Creds.Username = JsonObj->GetStringField(TEXT("username"));
		Creds.Password = JsonObj->GetStringField(TEXT("password"));
		Creds.TTL = JsonObj->GetIntegerField(TEXT("ttl"));

		const TArray<TSharedPtr<FJsonValue>>* UrisArray;
		if (JsonObj->TryGetArrayField(TEXT("uris"), UrisArray))
		{
			for (const auto& Uri : *UrisArray)
			{
				Creds.URIs.Add(Uri->AsString());
			}
		}

		OnTurnCredentialsReceived.Broadcast(Creds);
	}
}

// --- Health Check ---

void UMasterServerClient::CheckHealth()
{
	TSharedRef<IHttpRequest> Request = CreateRequest(TEXT("/api/health"), TEXT("GET"));
	Request->OnProcessRequestComplete().BindUObject(this, &UMasterServerClient::HandleHealthResponse);
	Request->ProcessRequest();
}

void UMasterServerClient::HandleHealthResponse(FHttpRequestPtr Request, FHttpResponsePtr Response, bool bSuccess)
{
	FServerHealth Health;

	if (!bSuccess || !Response.IsValid() || Response->GetResponseCode() != 200)
	{
		OnHealthCheckComplete.Broadcast(Health);
		return;
	}

	TSharedPtr<FJsonObject> JsonObj;
	TSharedRef<TJsonReader<>> Reader = TJsonReaderFactory<>::Create(Response->GetContentAsString());
	if (FJsonSerializer::Deserialize(Reader, JsonObj) && JsonObj.IsValid())
	{
		Health.bHealthy = JsonObj->GetStringField(TEXT("status")) == TEXT("ok");
		Health.Version = JsonObj->GetStringField(TEXT("version"));
		Health.Uptime = JsonObj->GetStringField(TEXT("uptime"));
		Health.ActiveGames = JsonObj->GetIntegerField(TEXT("active_games"));
		Health.TotalPlayers = JsonObj->GetIntegerField(TEXT("total_players"));
	}

	OnHealthCheckComplete.Broadcast(Health);
}

// --- Helpers ---

FGameInfo UMasterServerClient::ParseGameInfo(const TSharedPtr<FJsonObject>& JsonObj) const
{
	FGameInfo Info;
	if (!JsonObj.IsValid()) return Info;

	Info.Id = JsonObj->GetStringField(TEXT("id"));
	Info.Name = JsonObj->GetStringField(TEXT("name"));
	Info.JoinCode = JsonObj->GetStringField(TEXT("join_code"));
	Info.MaxPlayers = JsonObj->GetIntegerField(TEXT("max_players"));
	Info.CurrentPlayers = JsonObj->GetIntegerField(TEXT("current_players"));
	Info.NATType = JsonObj->GetStringField(TEXT("nat_type"));
	Info.bHasPassword = JsonObj->GetBoolField(TEXT("has_password"));
	Info.bPrivate = JsonObj->GetBoolField(TEXT("private"));
	Info.CreatedAt = JsonObj->GetStringField(TEXT("created_at"));

	const TSharedPtr<FJsonObject>* DataObj;
	if (JsonObj->TryGetObjectField(TEXT("data"), DataObj))
	{
		for (const auto& Pair : (*DataObj)->Values)
		{
			Info.Data.Add(Pair.Key, Pair.Value->AsString());
		}
	}

	return Info;
}
