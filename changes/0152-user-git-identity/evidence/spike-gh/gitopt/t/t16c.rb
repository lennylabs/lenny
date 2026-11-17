require 'grape'; require 'rack'; require 'rack/mock'
class A < Grape::API
  format :json
  prefix :api
  version 'v4', using: :path
  resource :projects do
    route_param :id, requirements: /[^\/]+/ do
      post('merge_requests/:iid/approve'){ {hit:'approve',id:params[:id]} }
      put('merge_requests/:iid/merge'){ {hit:'merge',id:params[:id]} }
    end
  end
end
m=Rack::MockRequest.new(A)
[['POST','/api/v4/projects/1/merge_requests/7/approve'],['POST','/api/v4/projects/acme/widgets/merge_requests/7/approve'],['POST','/api/v4/projects/1/merge_requests/7/approve.json.json'],['POST','/api/v4/projects/1/merge_requests/7/approve.foo'],['POST','/api/v4/projects/1/merge_requests/7/approve.JSON'],['PUT','/api/v4/projects/1/merge_requests/7/merge.json.json']].each{|v,p| r=m.request(v,p); puts "grape #{v.ljust(5)} #{p.ljust(62)} #{r.status} #{r.body[0,40]}" }
