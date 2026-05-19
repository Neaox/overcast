use aws_config::{BehaviorVersion, Region};
use aws_credential_types::Credentials;

pub struct AwsClients {
    endpoint: String,
    shared_config: aws_config::SdkConfig,
}

impl AwsClients {
    pub async fn new(endpoint: String, region: String) -> Self {
        let shared_config = aws_config::defaults(BehaviorVersion::latest())
            .region(Region::new(region))
            .credentials_provider(Credentials::new(
                "test",
                "test",
                None,
                None,
                "overcast-compat",
            ))
            .load()
            .await;

        Self {
            endpoint,
            shared_config,
        }
    }

    pub fn dynamodb(&self) -> aws_sdk_dynamodb::Client {
        let config = aws_sdk_dynamodb::config::Builder::from(&self.shared_config)
            .endpoint_url(&self.endpoint)
            .build();
        aws_sdk_dynamodb::Client::from_conf(config)
    }

    pub fn kms(&self) -> aws_sdk_kms::Client {
        let config = aws_sdk_kms::config::Builder::from(&self.shared_config)
            .endpoint_url(&self.endpoint)
            .build();
        aws_sdk_kms::Client::from_conf(config)
    }

    pub fn lambda(&self) -> aws_sdk_lambda::Client {
        let config = aws_sdk_lambda::config::Builder::from(&self.shared_config)
            .endpoint_url(&self.endpoint)
            .build();
        aws_sdk_lambda::Client::from_conf(config)
    }

    pub fn s3(&self) -> aws_sdk_s3::Client {
        let config = aws_sdk_s3::config::Builder::from(&self.shared_config)
            .endpoint_url(&self.endpoint)
            .force_path_style(true)
            .build();
        aws_sdk_s3::Client::from_conf(config)
    }

    pub fn secretsmanager(&self) -> aws_sdk_secretsmanager::Client {
        let config = aws_sdk_secretsmanager::config::Builder::from(&self.shared_config)
            .endpoint_url(&self.endpoint)
            .build();
        aws_sdk_secretsmanager::Client::from_conf(config)
    }

    pub fn sns(&self) -> aws_sdk_sns::Client {
        let config = aws_sdk_sns::config::Builder::from(&self.shared_config)
            .endpoint_url(&self.endpoint)
            .build();
        aws_sdk_sns::Client::from_conf(config)
    }

    pub fn sqs(&self) -> aws_sdk_sqs::Client {
        let config = aws_sdk_sqs::config::Builder::from(&self.shared_config)
            .endpoint_url(&self.endpoint)
            .build();
        aws_sdk_sqs::Client::from_conf(config)
    }

    pub fn ssm(&self) -> aws_sdk_ssm::Client {
        let config = aws_sdk_ssm::config::Builder::from(&self.shared_config)
            .endpoint_url(&self.endpoint)
            .build();
        aws_sdk_ssm::Client::from_conf(config)
    }

    pub fn sts(&self) -> aws_sdk_sts::Client {
        let config = aws_sdk_sts::config::Builder::from(&self.shared_config)
            .endpoint_url(&self.endpoint)
            .build();
        aws_sdk_sts::Client::from_conf(config)
    }
}
