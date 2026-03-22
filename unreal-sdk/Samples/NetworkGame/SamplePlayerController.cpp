#include "SamplePlayerController.h"
#include "NATClient.h"
#include "NATpunchthroughModule.h"

ASamplePlayerController::ASamplePlayerController()
{
	NATClient = CreateDefaultSubobject<UNATClient>(TEXT("NATClient"));
}

void ASamplePlayerController::BeginPlay()
{
	Super::BeginPlay();

	NATClient->ServerUrl = MasterServerUrl;
	NATClient->ApiKey = ApiKey;

	NATClient->OnConnectionEstablished.AddDynamic(this, &ASamplePlayerController::OnConnectionEstablished);
	NATClient->OnConnectionMethodDetermined.AddDynamic(this, &ASamplePlayerController::OnConnectionMethodDetermined);
	NATClient->OnError.AddDynamic(this, &ASamplePlayerController::OnNATError);
}

void ASamplePlayerController::EndPlay(const EEndPlayReason::Type EndPlayReason)
{
	Disconnect();
	Super::EndPlay(EndPlayReason);
}

void ASamplePlayerController::JoinByCode(const FString& JoinCode, const FString& Password)
{
	UE_LOG(LogNATPunchthrough, Log, TEXT("SamplePlayer: Joining game with code '%s'..."), *JoinCode);
	NATClient->JoinGame(JoinCode, Password);
}

void ASamplePlayerController::Disconnect()
{
	if (NATClient)
	{
		NATClient->StopGame();
	}
}

FString ASamplePlayerController::GetConnectionMethodString() const
{
	if (!NATClient) return TEXT("None");
	return ConnectionMethodToString(NATClient->ActiveConnectionMethod);
}

bool ASamplePlayerController::IsNATConnected() const
{
	return NATClient && NATClient->bIsConnected;
}

void ASamplePlayerController::OnConnectionEstablished(const FString& PeerEndpoint)
{
	UE_LOG(LogNATPunchthrough, Log, TEXT("SamplePlayer: Connected to host via %s"), *PeerEndpoint);

	if (GEngine)
	{
		GEngine->AddOnScreenDebugMessage(-1, 10.0f, FColor::Green,
			FString::Printf(TEXT("Connected to host: %s"), *PeerEndpoint));
	}
}

void ASamplePlayerController::OnConnectionMethodDetermined(EConnectionMethod Method, const FString& MethodName)
{
	UE_LOG(LogNATPunchthrough, Log, TEXT("SamplePlayer: Connection method: %s"), *MethodName);

	if (GEngine)
	{
		GEngine->AddOnScreenDebugMessage(-1, 10.0f, FColor::Yellow,
			FString::Printf(TEXT("Connection: %s"), *MethodName));
	}
}

void ASamplePlayerController::OnNATError(const FString& Error)
{
	UE_LOG(LogNATPunchthrough, Error, TEXT("SamplePlayer: Error: %s"), *Error);

	if (GEngine)
	{
		GEngine->AddOnScreenDebugMessage(-1, 10.0f, FColor::Red,
			FString::Printf(TEXT("NAT Error: %s"), *Error));
	}
}
