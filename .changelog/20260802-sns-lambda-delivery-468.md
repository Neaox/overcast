* [sns/lambda] a `lambda`-protocol subscription is now actually delivered to — `Publish` invokes the function with AWS's `Records[].Sns` event instead of dropping the message
* [sns] a delivery that fails is logged and moved to the subscription's `RedrivePolicy` dead-letter queue, for every protocol, rather than being swallowed
* [sns] notification `Timestamp` uses AWS's millisecond form (`2012-04-25T21:49:25.719Z`)
+ [web] the SNS topic detail view shows each subscription's live delivery state, and `lambda` is selectable when subscribing
