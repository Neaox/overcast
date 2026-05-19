pub mod dynamodb;
pub mod kms;
pub mod lambda;
pub mod s3;
pub mod secretsmanager;
pub mod sns;
pub mod sqs;
pub mod ssm;
pub mod sts;

use std::collections::HashMap;

use crate::harness::TestFn;

pub trait ServiceGroup {
    fn impls(&self) -> HashMap<String, TestFn>;
    fn setups(&self) -> HashMap<String, TestFn>;
    fn teardowns(&self) -> HashMap<String, TestFn>;
}
