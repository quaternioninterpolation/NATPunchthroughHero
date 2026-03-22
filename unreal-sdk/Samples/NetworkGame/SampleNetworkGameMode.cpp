#include "SampleNetworkGameMode.h"
#include "NATClient.h"
#include "NATpunchthroughModule.h"

ASampleNetworkGameMode::ASampleNetworkGameMode()
{
	// Create the NAT Client as a default subobject
	NATClient = CreateDefaultSubobject<UNATClient>(TEXT("NATClient"));
}

void ASampleNetworkGameMode::BeginPlay()
{
	Super::BeginPlay();

	// Configure the NAT Client
	NATClient->ServerUrl = MasterServerUrl;
	NATClient->ApiKey = ApiKey;

	// Bind events
	NATClient->OnGameHosted.AddDynamic(this, &ASampleNetworkGameMode::OnGameHosted);
	NATClient->OnConnectionEstablished.AddDynamic(this, &ASampleNetworkGameMode::OnConnectionEstablished);
	NATClient->OnPeerJoined.AddDynamic(this, &ASampleNetworkGameMode::OnPeerJoined);
	NATClient->OnError.AddDynamic(this, &ASampleNetworkGameMode::OnNATError);
	NATClient->OnNATTypeDetected.AddDynamic(this, &ASampleNetworkGameMode::OnNATTypeDetected);

	if (bHostOnStart)
	{
		HostNewGame();
	}
}

void ASampleNetworkGameMode::EndPlay(const EEndPlayReason::Type EndPlayReason)
{
	StopSession();
	Super::EndPlay(EndPlayReason);
}

void ASampleNetworkGameMode::HostNewGame()
{
	FGameRegistration Info;
	Info.Name = GameName;
	Info.MaxPlayers = MaxPlayers;
	Info.Password = Password;

	UE_LOG(LogNATPunchthrough, Log, TEXT("Sample: Hosting game '%s' (max %d players)..."), *GameName, MaxPlayers);
	NATClient->HostGame(Info);
}

void ASampleNetworkGameMode::JoinGameByCode(const FString& JoinCode)
{
	UE_LOG(LogNATPunchthrough, Log, TEXT("Sample: Joining game with code '%s'..."), *JoinCode);
	NATClient->JoinGame(JoinCode, Password);
}

void ASampleNetworkGameMode::StopSession()
{
	if (NATClient)
	{
		NATClient->StopGame();
	}
	UE_LOG(LogNATPunchthrough, Log, TEXT("Sample: Session stopped"));
}

FString ASampleNetworkGameMode::GetJoinCode() const
{
	return NATClient ? NATClient->JoinCode : TEXT("");
}

void ASampleNetworkGameMode::OnGameHosted(const FString& GameId, const FString& JoinCode, const FString& HostToken)
{
	UE_LOG(LogNATPunchthrough, Log, TEXT("=== GAME HOSTED ==="));
	UE_LOG(LogNATPunchthrough, Log, TEXT("  Game ID:   %s"), *GameId);
	UE_LOG(LogNATPunchthrough, Log, TEXT("  Join Code: %s"), *JoinCode);
	UE_LOG(LogNATPunchthrough, Log, TEXT("  Share the join code with other players!"));

	if (GEngine)
	{
		GEngine->AddOnScreenDebugMessage(-1, 15.0f, FColor::Green,
			FString::Printf(TEXT("Game Hosted! Join Code: %s"), *JoinCode));
	}
}

void ASampleNetworkGameMode::OnConnectionEstablished(const FString& PeerEndpoint)
{
	UE_LOG(LogNATPunchthrough, Log, TEXT("Sample: Connection established via %s"), *PeerEndpoint);

	if (GEngine)
	{
		GEngine->AddOnScreenDebugMessage(-1, 10.0f, FColor::Cyan,
			FString::Printf(TEXT("Connected: %s"), *PeerEndpoint));
	}
}

void ASampleNetworkGameMode::OnPeerJoined(const FString& PeerId)
{
	UE_LOG(LogNATPunchthrough, Log, TEXT("Sample: Peer joined: %s"), *PeerId);

	if (GEngine)
	{
		GEngine->AddOnScreenDebugMessage(-1, 10.0f, FColor::Blue,
			FString::Printf(TEXT("Peer Joined: %s"), *PeerId));
	}
}

void ASampleNetworkGameMode::OnNATError(const FString& Error)
{
	UE_LOG(LogNATPunchthrough, Error, TEXT("Sample: NAT Error: %s"), *Error);

	if (GEngine)
	{
		GEngine->AddOnScreenDebugMessage(-1, 10.0f, FColor::Red,
			FString::Printf(TEXT("Error: %s"), *Error));
	}
}

void ASampleNetworkGameMode::OnNATTypeDetected(ENATType NATType, const FString& NATTypeName)
{
	UE_LOG(LogNATPunchthrough, Log, TEXT("Sample: NAT Type Detected: %s"), *NATTypeName);

	if (GEngine)
	{
		GEngine->AddOnScreenDebugMessage(-1, 10.0f, FColor::Yellow,
			FString::Printf(TEXT("NAT Type: %s"), *NATTypeName));
	}
}
