#pragma once

#include "CoreMinimal.h"
#include "NATTypes.generated.h"

/** NAT type detected via STUN discovery. */
UENUM(BlueprintType)
enum class ENATType : uint8
{
	Unknown        UMETA(DisplayName = "Unknown"),
	Open           UMETA(DisplayName = "Open"),
	FullCone       UMETA(DisplayName = "Full Cone"),
	Moderate       UMETA(DisplayName = "Moderate"),
	PortRestricted UMETA(DisplayName = "Port Restricted"),
	Symmetric      UMETA(DisplayName = "Symmetric")
};

/** Method used to establish the peer connection. */
UENUM(BlueprintType)
enum class EConnectionMethod : uint8
{
	None      UMETA(DisplayName = "None"),
	Direct    UMETA(DisplayName = "Direct (UPnP)"),
	StunPunch UMETA(DisplayName = "STUN Punch"),
	TurnRelay UMETA(DisplayName = "TURN Relay")
};

/** Information for registering a new game on the master server. */
USTRUCT(BlueprintType)
struct NATPUNCHTHROUGH_API FGameRegistration
{
	GENERATED_BODY()

	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough")
	FString Name;

	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough")
	int32 MaxPlayers = 4;

	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough")
	int32 CurrentPlayers = 1;

	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough")
	FString NATType = TEXT("unknown");

	/** Optional password for the game. Will be hashed server-side. */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough")
	FString Password;

	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough")
	FString Map;

	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough")
	FString GameVersion;

	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough")
	int32 HostPort = 7777;

	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough")
	FString LocalIP;

	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough")
	int32 LocalPort = 7777;

	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough")
	bool bPrivate = false;

	/** Arbitrary game metadata (max 4KB JSON). */
	UPROPERTY(EditAnywhere, BlueprintReadWrite, Category = "NAT Punchthrough")
	TMap<FString, FString> Data;
};

/** Result from registering a game on the master server. */
USTRUCT(BlueprintType)
struct NATPUNCHTHROUGH_API FRegisterResult
{
	GENERATED_BODY()

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	bool bSuccess = false;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString GameId;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString JoinCode;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString HostToken;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString Error;
};

/** Public game info returned from the master server. */
USTRUCT(BlueprintType)
struct NATPUNCHTHROUGH_API FGameInfo
{
	GENERATED_BODY()

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString Id;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString Name;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString JoinCode;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	int32 MaxPlayers = 0;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	int32 CurrentPlayers = 0;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString NATType;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	bool bHasPassword = false;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	bool bPrivate = false;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString CreatedAt;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	TMap<FString, FString> Data;
};

/** TURN relay server credentials. */
USTRUCT(BlueprintType)
struct NATPUNCHTHROUGH_API FTurnCredentials
{
	GENERATED_BODY()

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString Username;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString Password;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	int32 TTL = 0;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	TArray<FString> URIs;

	bool IsValid() const { return !Username.IsEmpty() && !Password.IsEmpty(); }
};

/** Result of a UPnP port mapping attempt. */
USTRUCT(BlueprintType)
struct NATPUNCHTHROUGH_API FUPnPResult
{
	GENERATED_BODY()

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	bool bSuccess = false;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString ExternalIP;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	int32 ExternalPort = 0;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString Error;
};

/** Result of STUN NAT discovery. */
USTRUCT(BlueprintType)
struct NATPUNCHTHROUGH_API FStunResult
{
	GENERATED_BODY()

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	bool bSuccess = false;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString PublicIP;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	int32 PublicPort = 0;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	ENATType NATType = ENATType::Unknown;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString Error;
};

/** Result of a hole punch attempt. */
USTRUCT(BlueprintType)
struct NATPUNCHTHROUGH_API FPunchResult
{
	GENERATED_BODY()

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	bool bSuccess = false;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString RemoteEndpoint;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString Error;
};

/** Server health check response. */
USTRUCT(BlueprintType)
struct NATPUNCHTHROUGH_API FServerHealth
{
	GENERATED_BODY()

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	bool bHealthy = false;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString Version;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString Uptime;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	int32 ActiveGames = 0;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	int32 TotalPlayers = 0;
};

/** ICE candidate exchanged during signaling. */
USTRUCT(BlueprintType)
struct NATPUNCHTHROUGH_API FICECandidate
{
	GENERATED_BODY()

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString PublicIP;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	int32 PublicPort = 0;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString LocalIP;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	int32 LocalPort = 0;

	UPROPERTY(BlueprintReadOnly, Category = "NAT Punchthrough")
	FString NATTypeString;
};

/** Helper to convert ENATType to the string the server expects. */
inline FString NATTypeToString(ENATType Type)
{
	switch (Type)
	{
	case ENATType::Open:           return TEXT("open");
	case ENATType::FullCone:       return TEXT("full_cone");
	case ENATType::Moderate:       return TEXT("moderate");
	case ENATType::PortRestricted: return TEXT("port_restricted");
	case ENATType::Symmetric:      return TEXT("symmetric");
	default:                       return TEXT("unknown");
	}
}

/** Helper to convert ConnectionMethod to the string the server expects. */
inline FString ConnectionMethodToString(EConnectionMethod Method)
{
	switch (Method)
	{
	case EConnectionMethod::Direct:    return TEXT("direct");
	case EConnectionMethod::StunPunch: return TEXT("punched");
	case EConnectionMethod::TurnRelay: return TEXT("relayed");
	default:                           return TEXT("none");
	}
}
