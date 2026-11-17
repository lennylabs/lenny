require 'rack'; require 'rack/mock'; require 'action_dispatch'; require 'action_controller'
class C < ActionController::Metal; def merge; self.response_body='merge'; end; def reviews; self.response_body='reviews'; end; end
R=ActionDispatch::Routing::RouteSet.new
R.draw{ put '/repos/:o/:r/pulls/:n/merge', to: C.action(:merge); put '/repos/:o/:r/branches/:b/protection', to: C.action(:merge) }
m=Rack::MockRequest.new(R)
['/repos/a/b/pulls/1/merge','/repos/a/b/pulls/1/merge.json','/repos/a/b/pulls/1/merge.json.json','/repos/a/b/pulls/1/merge.foo','/repos/a/b/pulls/1/merge.JSON','/repos/a/b/branches/main%2Fprotection','/repos/a/b/branches/a%2Fb/protection','/repos/a/b/branches/a/b/protection'].each{|p| r=m.request('PUT',p); puts "rails PUT #{p.ljust(42)} #{r.status} #{r.body[0,20]}" }
