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
    /// The service file these registrations come from. It is what a
    /// duplicate-key error names, so a collision points at the two files to
    /// look in rather than just the key they disagree about.
    fn name(&self) -> &'static str;
    fn impls(&self) -> HashMap<String, TestFn>;
    fn setups(&self) -> HashMap<String, TestFn>;
    fn teardowns(&self) -> HashMap<String, TestFn>;
}
